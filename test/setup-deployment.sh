#!/bin/bash

set -o errexit
set -o pipefail

TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname $(readlink -f $0))}
source ${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}

CLUSTER=${CLUSTER:-clear-govm}
REPO_DIRECTORY="${REPO_DIRECTORY:-$(dirname $(dirname $(readlink -f $0)))}"
WORK_DIRECTORY="${WORK_DIRECTORY:-${REPO_DIRECTORY}/_work/${CLUSTER}}"
KUBECTL="${KUBECTL:-${WORK_DIRECTORY}/ssh-${CLUSTER} kubectl}"
KUBERNETES_VERSION="$(${KUBECTL} version --short | grep 'Server Version' | \
        sed -e 's/.*: v\([0-9]*\)\.\([0-9]*\)\..*/\1.\2/')"
DEPLOYMENT_DIRECTORY="${REPO_DIRECTORY}/deploy/kubernetes-$KUBERNETES_VERSION"
DEPLOYMENT_FILES=(
    pmem-csi-${TEST_DEVICEMODE}-testing.yaml
    pmem-storageclass-ext4.yaml
    pmem-storageclass-xfs.yaml
    pmem-storageclass-cache.yaml
)

echo "$KUBERNETES_VERSION" > $WORK_DIRECTORY/kubernetes.version
for deployment_file in ${DEPLOYMENT_FILES[@]}; do
    sed "s|PMEM_REGISTRY|${TEST_PMEM_REGISTRY}|g" ${DEPLOYMENT_DIRECTORY}/${deployment_file} | ${KUBECTL} apply -f -
done
