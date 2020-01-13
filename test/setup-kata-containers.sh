#!/bin/bash

set -o errexit
set -o pipefail

TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname $(readlink -f $0))}
source ${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}

CLUSTER=${CLUSTER:-pmem-govm}
REPO_DIRECTORY="${REPO_DIRECTORY:-$(dirname $(dirname $(readlink -f $0)))}"
CLUSTER_DIRECTORY="${CLUSTER_DIRECTORY:-${REPO_DIRECTORY}/_work/${CLUSTER}}"
SSH="${CLUSTER_DIRECTORY}/ssh.0"
KUBECTL="${SSH} kubectl" # Always use the kubectl installed in the cluster.

curl --location --fail --silent \
     https://github.com/kata-containers/packaging/raw/${TEST_KATA_CONTAINERS_VERSION}/kata-deploy/kata-rbac/base/kata-rbac.yaml |
    ${KUBECTL} apply -f -

# kata-deploy.yaml always installs the latest Kata Containers. We override that
# here by locking the image to the specific version that we want.
curl --location --fail --silent \
     https://github.com/kata-containers/packaging/raw/${TEST_KATA_CONTAINERS_VERSION}/kata-deploy/kata-deploy/base/kata-deploy.yaml |
    sed -e "s;image: katadocker/kata-deploy.*;image: katadocker/kata-deploy:${TEST_KATA_CONTAINERS_VERSION};" |
    ${KUBECTL} apply -f -

curl --location --fail --silent \
     https://raw.githubusercontent.com/kata-containers/packaging/${TEST_KATA_CONTAINERS_VERSION}/kata-deploy/k8s-1.14/kata-qemu-runtimeClass.yaml |
    ${KUBECTL} apply -f -

echo "Waiting for kata-deploy to label nodes..."
TIMEOUT=120
while [ "$SECONDS" -lt "$TIMEOUT" ]; do
    # We either get "No resources found in default namespace." or header plus node names.
    if [ $(${KUBECTL} get nodes -l katacontainers.io/kata-runtime=true 2>&1 | wc -l) -gt 1 ]; then
        echo "Kata Containers runtime available on:"
        ${KUBECTL} get nodes -l katacontainers.io/kata-runtime=true
        exit 0
    fi
done

echo "kata-deploy has not labelled nodes after $TIMEOUT seconds. Is the container runtime perhaps Docker? It is not supported."
exit 1
