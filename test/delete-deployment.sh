#!/bin/bash

set -o errexit

TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname $(readlink -f $0))}
source ${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}

CLUSTER=${CLUSTER:-pmem-govm}
REPO_DIRECTORY="${REPO_DIRECTORY:-$(dirname $(dirname $(readlink -f $0)))}"
CLUSTER_DIRECTORY="${CLUSTER_DIRECTORY:-${REPO_DIRECTORY}/_work/${CLUSTER}}"
SSH="${CLUSTER_DIRECTORY}/ssh.0"
KUBECTL="${SSH} kubectl" # Always use the kubectl installed in the cluster.

kinds="
      deployments
      replicasets
      statefulsets
      daemonsets

      clusterrolebindings
      clusterroles
      crd
      csidrivers
      mutatingwebhookconfigurations
      pods
      rolebindings
      roles
      serviceaccounts
      services
      storageclasses
"
for kind in $kinds; do
    echo -n "$kind: "
    ${KUBECTL} delete --all-namespaces -l pmem-csi.intel.com/deployment $kind
    ${KUBECTL} delete --all-namespaces -l app.kubernetes.io/part-of=pmem-csi $kind
done
