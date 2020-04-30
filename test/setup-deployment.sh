#!/bin/bash

set -o errexit
set -o pipefail

# This reads a file and encodes it for use in a secret.
read_key () {
    base64 -w 0 "$1"
}

TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname $(readlink -f $0))}
source ${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}

CLUSTER=${CLUSTER:-pmem-govm}
REPO_DIRECTORY="${REPO_DIRECTORY:-$(dirname $(dirname $(readlink -f $0)))}"
CLUSTER_DIRECTORY="${CLUSTER_DIRECTORY:-${REPO_DIRECTORY}/_work/${CLUSTER}}"
SSH="${CLUSTER_DIRECTORY}/ssh.0"
KUBECTL="${SSH} kubectl" # Always use the kubectl installed in the cluster.
KUBERNETES_VERSION="$(cat "$CLUSTER_DIRECTORY/kubernetes.version")"
DEPLOYMENT_DIRECTORY="${REPO_DIRECTORY}/deploy/kubernetes-$KUBERNETES_VERSION"
case ${TEST_DEPLOYMENTMODE} in
    testing)
        deployment_suffix="/testing";;
    production)
        deployment_suffix="";;
    *)
        echo >&2 "invalid TEST_DEPLOYMENTMODE: ${TEST_DEPLOYMENTMODE}"
        exit 1
esac
DEPLOY=(
    ${TEST_DEVICEMODE}${deployment_suffix}
    pmem-storageclass-ext4.yaml
    pmem-storageclass-xfs.yaml
    pmem-storageclass-cache.yaml
    pmem-storageclass-late-binding.yaml
    scheduler
    webhook
)

# Read certificate files and turn them into Kubernetes secrets.
#
# -caFile (controller and all nodes)
CA=$(read_key "${TEST_CA}")
# -certFile (controller)
REGISTRY_CERT=$(read_key "${TEST_REGISTRY_CERT}")
# -keyFile (controller)
REGISTRY_KEY=$(read_key "${TEST_REGISTRY_KEY}")
# -certFile (same for all nodes)
NODE_CERT=$(read_key "${TEST_NODE_CERT}")
# -keyFile (same for all nodes)
NODE_KEY=$(read_key "${TEST_NODE_KEY}")

${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
    name: pmem-csi-registry-secrets
    labels:
        pmem-csi.intel.com/deployment: ${TEST_DEVICEMODE}-${TEST_DEPLOYMENTMODE}
type: kubernetes.io/tls
data:
    ca.crt: ${CA}
    tls.crt: ${REGISTRY_CERT}
    tls.key: ${REGISTRY_KEY}
---
apiVersion: v1
kind: Secret
metadata:
    name: pmem-csi-node-secrets
    labels:
        pmem-csi.intel.com/deployment: ${TEST_DEVICEMODE}-${TEST_DEPLOYMENTMODE}
type: Opaque
data:
    ca.crt: ${CA}
    tls.crt: ${NODE_CERT}
    tls.key: ${NODE_KEY}
EOF

case "$KUBERNETES_VERSION" in
    1.1[01234])
        # We cannot exclude the PMEM-CSI pods from the webhook because objectSelector
        # was only added in 1.15. Instead, we exclude the entire "default" namespace.
        # This means our normal test applications also don't use it, but our normal
        # instructions for checking that PMEM-CSI works still apply.
        ${KUBECTL} label --overwrite ns default pmem-csi.intel.com/webhook=ignore
        ;;
esac

for deploy in ${DEPLOY[@]}; do
    path="${DEPLOYMENT_DIRECTORY}/${deploy}"
    if [ -f "$path" ]; then
        ${KUBECTL} apply -f - <"$path"
    elif [ -d "$path" ]; then
        # A kustomize base. We need to copy all files over into the cluster, otherwise
        # `kubectl kustomize` won't work.
        tmpdir=$(${SSH} mktemp -d)
        case "$path" in /*) tar -C / -chf - "$(echo "$path" | sed -e 's;^/;;')";;
                         *) tar -chf - "$path";;
        esac | ${SSH} tar -xf - -C "$tmpdir"
        if [ -f "$path/pmem-csi.yaml" ]; then
            # Replace registry. This is easier with sed than kustomize...
            ${SSH} sed -i -e "s^intel/pmem^${TEST_PMEM_REGISTRY}/pmem^g" "$tmpdir/$path/pmem-csi.yaml"
        fi
        ${SSH} mkdir "$tmpdir/my-deployment"
        ${SSH} "cat >'$tmpdir/my-deployment/kustomization.yaml'" <<EOF
bases:
  - ../$path
EOF
        case $deploy in
            ${TEST_DEVICEMODE}${deployment_suffix})
                ${SSH} "cat >>'$tmpdir/my-deployment/kustomization.yaml'" <<EOF
patchesJson6902:
  - target:
      group: apps
      version: v1
      kind: StatefulSet
      name: pmem-csi-controller
    path: controller-patch.yaml
EOF
                ${SSH} "cat >'$tmpdir/my-deployment/controller-patch.yaml'" <<EOF
- op: add
  path: /spec/template/spec/containers/0/command/-
  value: "--schedulerListen=:8000" # Exposed to kube-scheduler via the pmem-csi-scheduler service.
- op: add
  path: /spec/template/spec/affinity
  value:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          # Do *not* run controller on worker nodes with PMEM. This is
          # a workaround for a particular issue on Clear Linux where network
          # configuration randomly fails such that the driver which runs on the same
          # node as the controller cannot connect to the controller
          # (https://github.com/intel/pmem-csi/issues/555).
          - key: storage
            operator: NotIn
            values:
            - pmem
- op: add
  path: /spec/template/spec/tolerations
  value:
    - key: "node-role.kubernetes.io/master"
      operator: "Exists"
      effect: "NoSchedule"
EOF
                if [ "${TEST_DEVICEMODE}" = "lvm" ]; then
                    # Test these options and kustomization by injecting some non-default values.
                    # This could be made optional to test both default and non-default values,
                    # but for now we just change this in all deployments.
                    ${SSH} "cat >>'$tmpdir/my-deployment/kustomization.yaml'" <<EOF
  - target:
      group: apps
      version: v1
      kind: DaemonSet
      name: pmem-csi-node
    path: lvm-parameters-patch.yaml
EOF
                    ${SSH} "cat >'$tmpdir/my-deployment/lvm-parameters-patch.yaml'" <<EOF
- op: add
  path: /spec/template/spec/initContainers/0/command/-
  value: "--useforfsdax=50"
EOF
                fi
                ;;
            scheduler)
                # Change port number via JSON patch.
                ${SSH} "cat >>'$tmpdir/my-deployment/kustomization.yaml'" <<EOF
commonLabels:
  pmem-csi.intel.com/deployment: ${TEST_DEVICEMODE}-${TEST_DEPLOYMENTMODE}
patchesJson6902:
  - target:
      version: v1
      kind: Service
      name: pmem-csi-scheduler
    path: scheduler-patch.yaml
EOF
                ${SSH} "cat >'$tmpdir/my-deployment/scheduler-patch.yaml'" <<EOF
- op: add
  path: /spec/ports/0/nodePort
  value: ${TEST_SCHEDULER_EXTENDER_NODE_PORT}
EOF
                ;;
            webhook)
                ${SSH} "cat >>'$tmpdir/my-deployment/kustomization.yaml'" <<EOF
commonLabels:
  pmem-csi.intel.com/deployment: ${TEST_DEVICEMODE}-${TEST_DEPLOYMENTMODE}
patchesJson6902:
  - target:
      group: admissionregistration.k8s.io
      version: v1beta1
      kind: MutatingWebhookConfiguration
      name: pmem-csi-hook
    path: webhook-patch.yaml
EOF
                ${SSH} "cat >'$tmpdir/my-deployment/webhook-patch.yaml'" <<EOF
- op: replace
  path: /webhooks/0/clientConfig/caBundle
  value: $(base64 -w 0 _work/pmem-ca/ca.pem)
EOF
                ;;
        esac
        ${KUBECTL} apply --kustomize "$tmpdir/my-deployment"
        ${SSH} rm -rf "$tmpdir"
    else
        echo >&2 "$path is missing."
        exit 1
    fi
done

${KUBECTL} label --overwrite ns kube-system pmem-csi.intel.com/webhook=ignore

cat <<EOF

To try out the pmem-csi driver persistent volumes:
   cat deploy/kubernetes-${KUBERNETES_VERSION}/pmem-pvc.yaml | ${KUBECTL} create -f -
   cat deploy/kubernetes-${KUBERNETES_VERSION}/pmem-app.yaml | ${KUBECTL} create -f -

To try out the pmem-csi driver cache volumes:
   cat deploy/kubernetes-${KUBERNETES_VERSION}/pmem-pvc-cache.yaml | ${KUBECTL} create -f -
   cat deploy/kubernetes-${KUBERNETES_VERSION}/pmem-app-cache.yaml | ${KUBECTL} create -f -
EOF

if [ -e ${DEPLOYMENT_DIRECTORY}/pmem-storageclass-late-binding.yaml ]; then
    cat <<EOF

To try out the pmem-csi driver persistent volumes with late binding:
   cat deploy/kubernetes-${KUBERNETES_VERSION}/pmem-pvc-late-binding.yaml | ${KUBECTL} create -f -
   cat deploy/kubernetes-${KUBERNETES_VERSION}/pmem-app-late-binding.yaml | ${KUBECTL} create -f -
EOF
fi

if [ -e ${DEPLOYMENT_DIRECTORY}/pmem-app-ephemeral.yaml ]; then
    cat <<EOF

To try out the pmem-csi driver ephemeral volumes:
   cat deploy/kubernetes-${KUBERNETES_VERSION}/pmem-app-ephemeral.yaml | ${KUBECTL} create -f -
EOF
fi
