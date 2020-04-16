#!/bin/bash

set -o errexit
set -o pipefail

TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname "$(readlink -f "$0")")}
source "${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}"

CLUSTER=${CLUSTER:-pmem-govm}
REPO_DIRECTORY="${REPO_DIRECTORY:-$(dirname "${TEST_DIRECTORY}")}"
CLUSTER_DIRECTORY="${CLUSTER_DIRECTORY:-${REPO_DIRECTORY}/_work/${CLUSTER}}"
DEPLOYMENT_DIRECTORY="${REPO_DIRECTORY}/operator/deploy"
SSH="${CLUSTER_DIRECTORY}/ssh.0"
KUBECTL="${SSH} kubectl" # Always use the kubectl installed in the cluster.

deploy="${DEPLOYMENT_DIRECTORY}/operator.yaml"
echo "Deploying '${deploy}'..."

if [ -f "$deploy" ]; then
  tmpdir=$(${SSH} mktemp -d)
  trap '${SSH} "echo cleaning up temp folder: $tmpdir; rm -rf $tmpdir"' SIGTERM SIGINT EXIT

  ${SSH} "cat > '$tmpdir/operator.yaml'" <<EOF
$(cat "${deploy}")
EOF

  if [ "${TEST_PMEM_REGISTRY}" != "" ]; then
     ${SSH} sed -ie "s^intel/pmem^${TEST_PMEM_REGISTRY}/pmem^g" "$tmpdir/operator.yaml"
  fi
  ${SSH} "cat >'$tmpdir/kustomization.yaml'" <<EOF
resources:
- operator.yaml
commonLabels:
  pmem-csi.intel.com/deployment: ${TEST_OPERATOR_DEPLOYMENT}
EOF

  ${SSH} "cat >>'$tmpdir/kustomization.yaml'" <<EOF
namespace: "${TEST_OPERATOR_NAMESPACE}"
patchesJson6902:
- target:
    group: rbac.authorization.k8s.io
    version: v1
    kind: ClusterRoleBinding
    name: pmem-csi-operator
  path: crb-sa-namespace-patch.json
EOF
  ${SSH} "cat >'$tmpdir/crb-sa-namespace-patch.json'" <<EOF
- op: replace
  path: /subjects/0/namespace
  value: "${TEST_OPERATOR_NAMESPACE}"
EOF

  ${KUBECTL} apply --kustomize "$tmpdir"

  cat <<EOF
PMEM-CSI operator is running. To try out deploying the pmem-csi driver:
    cat operator/examples/pmem-csi.intel.com_v1alpha1_deployment_cr.yaml | ${KUBECTL} create -f -
EOF
else
  echo >&2 "'${deploy}' not a yaml file"
fi
