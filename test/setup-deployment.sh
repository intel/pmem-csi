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
case ${TEST_DEPLOYMENTMODE} in
    testing)
        deployment_suffix="-testing";;
    production)
        deployment_suffix="";;
    *)
        echo >&2 "invalid TEST_DEPLOYMENTMODE: ${TEST_DEPLOYMENTMODE}"
        exit 1
esac
DEPLOYMENT_FILES=(
    pmem-csi-${TEST_DEVICEMODE}${deployment_suffix}.yaml
    pmem-storageclass-ext4.yaml
    pmem-storageclass-xfs.yaml
    pmem-storageclass-cache.yaml
    pmem-storageclass-late-binding.yaml
)

echo "$KUBERNETES_VERSION" > $WORK_DIRECTORY/kubernetes.version
for deployment_file in ${DEPLOYMENT_FILES[@]}; do
    if [ -e ${DEPLOYMENT_DIRECTORY}/${deployment_file} ]; then
        sed "s|PMEM_REGISTRY|${TEST_PMEM_REGISTRY}|g" ${DEPLOYMENT_DIRECTORY}/${deployment_file} | ${KUBECTL} apply -f -
    fi
done

cat <<EOF

The test cluster is ready. Log in with ${WORK_DIRECTORY}/ssh-${CLUSTER}, run kubectl once logged in.
Alternatively, KUBECONFIG=${WORK_DIRECTORY}/kube.config can also be used directly.

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
