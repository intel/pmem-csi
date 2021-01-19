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
DEPLOYMENT_DIRECTORY="${REPO_DIRECTORY}/deploy/kubernetes-$KUBERNETES_VERSION${TEST_KUBERNETES_FLAVOR}"
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
    pmem-storageclass-ext4-kata.yaml
    pmem-storageclass-xfs.yaml
    pmem-storageclass-xfs-kata.yaml
    pmem-storageclass-cache.yaml
    pmem-storageclass-late-binding.yaml
    scheduler
    webhook
)
echo "INFO: deploying from ${DEPLOYMENT_DIRECTORY}/${TEST_DEVICEMODE}${deployment_suffix}"

# Set up TLS secrets in the TEST_DRIVER_NAMESPACE.
PATH="${REPO_DIRECTORY}/_work/bin:$PATH" KUBECTL="${KUBECTL}" ${TEST_DIRECTORY}/setup-ca-kubernetes.sh

case "$KUBERNETES_VERSION" in
    1.1[01234])
        # We cannot exclude the PMEM-CSI pods from the webhook because objectSelector
        # was only added in 1.15. Instead, we exclude the entire "TEST_DRIVER_NAMESPACE" namespace.
        # This means our normal test applications also don't use it, but our normal
        # instructions for checking that PMEM-CSI works still apply.
        ${KUBECTL} label --overwrite ns ${TEST_DRIVER_NAMESPACE} pmem-csi.intel.com/webhook=ignore
        ;;
esac

for deploy in ${DEPLOY[@]}; do
    # Deployment files can come from:
    # 1. deploy/kubernetes-*
    # 2. deploy/common
    # 3. deploy/kustomize directly
    path="${DEPLOYMENT_DIRECTORY}/${deploy}"
    paths="$path"
    if ! [ -e "$path" ]; then
        path="${REPO_DIRECTORY}/deploy/common/${deploy}"
        paths+=" $path"
    fi
    if ! [ -e "$path" ]; then
        path="${REPO_DIRECTORY}/deploy/kustomize/${deploy}"
        paths+=" $path"
    fi
    if [ -f "$path" ]; then
        case "$path" in
            *storageclass*)
                # Patch the node selector label into the storage class instead of the default storage=pmem.
                sed -e "s;: storage\$;: \"$(echo $TEST_PMEM_NODE_LABEL | cut -d= -f1)\";" \
                    -e "s;- pmem\$;- \"$(echo $TEST_PMEM_NODE_LABEL | cut -d= -f2)\";" \
                    "$path" | ${KUBECTL} apply -f -
                ;;
            *)
                ${KUBECTL} apply -f - <"$path"
                ;;
            esac
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
            # Replace Namespace object name
            ${SSH} "sed -ie 's;\(name: \)pmem-csi$;\1${TEST_DRIVER_NAMESPACE};g' $tmpdir/$path/pmem-csi.yaml"
            # Same for image pull policy.
            ${SSH} <<EOF
sed -i -e "s^imagePullPolicy:.IfNotPresent^imagePullPolicy: ${TEST_IMAGE_PULL_POLICY}^g" "$tmpdir/$path/pmem-csi.yaml"
EOF
        fi
        ${SSH} mkdir "$tmpdir/my-deployment"
        trap '${SSH} "rm -rf $tmpdir"' SIGTERM SIGINT EXIT
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
      name: pmem-csi-intel-com-controller
    path: scheduler-patch.yaml
EOF
                ${SSH} "cat >'$tmpdir/my-deployment/scheduler-patch.yaml'" <<EOF
- op: add
  path: /spec/template/spec/containers/0/command/-
  value: "--schedulerListen=:8000" # Exposed to kube-scheduler via the pmem-csi-scheduler service.
- op: add
  path: /spec/template/spec/containers/0/command/-
  value: -nodeSelector={$(echo ${TEST_PMEM_NODE_LABEL} | sed -e 's/\([^=]*\)=\(.*\)/"\1":"\2"/')}
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
      name: pmem-csi-intel-com-node
    path: lvm-parameters-patch.yaml
EOF
                    ${SSH} "cat >'$tmpdir/my-deployment/lvm-parameters-patch.yaml'" <<EOF
- op: add
  path: /spec/template/spec/containers/0/command/-
  value: "--pmemPercentage=50"
EOF
                fi

                # Always use the configured label for selecting nodes.
                ${SSH} "cat >>'$tmpdir/my-deployment/kustomization.yaml'" <<EOF
  - target:
      group: apps
      version: v1
      kind: DaemonSet
      name: pmem-csi-intel-com-node
    path: node-label-patch.yaml
EOF
                ${SSH} "cat >>'$tmpdir/my-deployment/node-label-patch.yaml'" <<EOF
- op: add
  path: /spec/template/spec/nodeSelector
  value:
     {$(echo "${TEST_PMEM_NODE_LABEL}" | sed -e 's/\(.*\)=\(.*\)/\1: "\2"/')}
EOF
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
      name: pmem-csi-intel-com-scheduler
    path: scheduler-patch.yaml
EOF
                ${SSH} "cat >'$tmpdir/my-deployment/scheduler-patch.yaml'" <<EOF
- op: add
  path: /spec/ports/0/nodePort
  value: ${TEST_SCHEDULER_EXTENDER_NODE_PORT}
- op: add
  path: /spec/type
  value: NodePort
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
      name: pmem-csi-intel-com-hook
    path: webhook-patch.yaml
EOF
                ${SSH} "cat >'$tmpdir/my-deployment/webhook-patch.yaml'" <<EOF
- op: replace
  path: /webhooks/0/clientConfig/caBundle
  value: $(base64 -w 0 ${TEST_CA}.pem)
- op: replace
  path: /webhooks/0/clientConfig/service/namespace
  value: ${TEST_DRIVER_NAMESPACE}
- op: replace
  path: /webhooks/0/failurePolicy
  value: Fail # This is not the default anymore in PMEM-CSI, but for testing we want it.
EOF
                ;;
        esac

        ${SSH} "cat >>'$tmpdir/my-deployment/kustomization.yaml'" <<EOF
namespace: ${TEST_DRIVER_NAMESPACE}
EOF
        # When quickly taking down one installation of PMEM-CSI and recreating it, sometimes we get:
        #   nodePort: Invalid value: 32000: provided port is already allocated
        #
        # A fix is going into 1.19: https://github.com/kubernetes/kubernetes/pull/89937/commits
        # Not sure whether that is applicable here because we don't use a HA setup and
        # besides, we also need to support older Kubernetes releases. Therefore we retry...
        start=$SECONDS
        while ! output="$(${KUBECTL} apply --kustomize "$tmpdir/my-deployment" 2>&1)"; do
            if echo "$output" | grep -q "nodePort: Invalid value: ${TEST_SCHEDULER_EXTENDER_NODE_PORT}: provided port is already allocated" &&
                    [ $(($SECONDS - $start)) -lt 60 ]; then
                # Retry later...
                echo "Warning: kubectl failed with potentially temporary error, will try again: $output"
                sleep 1
            else
                echo "$output"
                exit 1
            fi
        done
        echo "$output"
        ${SSH} rm -rf "$tmpdir"
    else
        case "$path" in
            */scheduler|*/webhook)
                # optional, continue
                :
                ;;
            *)
                # Should be there, fail.
                echo >&2 "$paths are all missing."
                exit 1
                ;;
        esac
    fi
done

${KUBECTL} label --overwrite ns kube-system pmem-csi.intel.com/webhook=ignore

if [ "${TEST_DEPLOYMENT_QUIET}" = "" ]; then
    cat <<EOF

To try out the PMEM-CSI driver with persistent volumes:
   cat deploy/common/pmem-pvc.yaml | ${KUBECTL} create -f -
   cat deploy/common/pmem-app.yaml | ${KUBECTL} create -f -

To try out the PMEM-CSI driver with cache volumes:
   cat deploy/common/pmem-pvc-cache.yaml | ${KUBECTL} create -f -
   cat deploy/common/pmem-app-cache.yaml | ${KUBECTL} create -f -

To try out the PMEM-CSI driver with persistent volumes that use late binding:
   cat deploy/common/pmem-pvc-late-binding.yaml | ${KUBECTL} create -f -
   cat deploy/common/pmem-app-late-binding.yaml | ${KUBECTL} create -f -

To try out the PMEM-CSI driver with ephemeral volumes:
   cat deploy/common/pmem-app-ephemeral.yaml | ${KUBECTL} create -f -
EOF
fi
