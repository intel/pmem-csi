#!/bin/bash

set -o errexit

TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname $(readlink -f $0))}
source ${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}

CLUSTER=${CLUSTER:-pmem-govm}
REPO_DIRECTORY="${REPO_DIRECTORY:-$(dirname $(dirname $(readlink -f $0)))}"
CLUSTER_DIRECTORY="${CLUSTER_DIRECTORY:-${REPO_DIRECTORY}/_work/${CLUSTER}}"
SSH="${CLUSTER_DIRECTORY}/ssh.0"
KUBECTL="${SSH} kubectl" # Always use the kubectl installed in the cluster.
VERSION="${TEST_KATA_CONTAINERS_VERSION:-2.1.0}"

${KUBECTL} apply -f https://github.com/kata-containers/kata-containers/raw/${VERSION}/tools/packaging/kata-deploy/kata-rbac/base/kata-rbac.yaml
${KUBECTL} apply -f https://github.com/kata-containers/kata-containers/raw/${VERSION}/tools/packaging/kata-deploy/kata-deploy/base/kata-deploy.yaml
${KUBECTL} apply -f https://github.com/kata-containers/kata-containers/raw/${VERSION}/tools/packaging/kata-deploy/runtimeclasses/kata-runtimeClasses.yaml

echo "Waiting for kata-deploy to label nodes..."
TIMEOUT=300
while [ "$SECONDS" -lt "$TIMEOUT" ]; do
    # We either get "No resources found in default namespace." or header plus node names.
    if [ $(${KUBECTL} get nodes -l katacontainers.io/kata-runtime=true 2>&1 | wc -l) -gt 1 ]; then
        echo "Kata Containers runtime available on:"
        ${KUBECTL} get nodes -l katacontainers.io/kata-runtime=true
        exit 0
    fi
    sleep 1
done

echo "kata-deploy has not labelled nodes after $TIMEOUT seconds. Is the container runtime perhaps Docker? It is not supported."
exit 1
