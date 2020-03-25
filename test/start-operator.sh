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

# We not yet support kustomized operator deployment
if [ -f "$deploy" ]; then
    if [ "${TEST_PMEM_REGISTRY}" != "" ]; then
      set -x
      sed -e "s^intel/pmem^${TEST_PMEM_REGISTRY}/pmem^g" "${deploy}" | ${KUBECTL} apply -f -
    fi

    cat <<EOF
PMEM-CSI operator is running. To try out deploying the pmem-csi driver:
    cat operator/examples/pmem-csi.intel.com_v1alpha1_deployment_cr.yaml | ${KUBECTL} create -f -
EOF
fi
