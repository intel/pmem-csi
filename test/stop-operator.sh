#!/bin/bash
#
# Script to delete a running operator deployment.
#
set -o errexit
set -o pipefail

TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname "$(readlink -f "$0")")}
source "${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}"

REPO_DIRECTORY="${REPO_DIRECTORY:-$(dirname "${TEST_DIRECTORY}")}"
CLUSTER=${CLUSTER:-pmem-govm}
CLUSTER_DIRECTORY="${CLUSTER_DIRECTORY:-${REPO_DIRECTORY}/_work/${CLUSTER}}"
SSH="${CLUSTER_DIRECTORY}/ssh.0"
KUBECTL="${SSH} kubectl" # Always use the kubectl installed in the cluster.

function delete_olm_operator() {
  set -x
  BINDIR=${REPO_DIRECTORY}/_work/bin
  CATALOG_DIR="${REPO_DIRECTORY}/deploy/olm-catalog"

  if [ ! -d "${CATALOG_DIR}" ]; then
    echo >&2 "'${CATALOG_DIR}' not a directory"
    return 1
  fi

  VERSION=$(grep 'currentCSV:' ${CATALOG_DIR}/pmem-csi-operator.package.yaml | sed -r 's|.*currentCSV: (.*)|\1|')

  set -e
  output=$(${KUBECTL} get clusterserviceversion ${VERSION} 2>&1)
  if echo $oupput | grep -q '(NotFound)' ; then
    echo "Operator deployment not found!"
    exit 0
  fi
  set +e
 
  namespace=""
  if [ "${TEST_OPERATOR_NAMESPACE}" != "" ]; then
    namespace="--namespace ${TEST_OPERATOR_NAMESPACE}"
  fi

  echo "Cleaning up the operator deployment using OLM"
  ${BINDIR}/operator-sdk cleanup pmem-csi-operator $namespace
}

function delete_operator() {
  DEPLOY_DIRECTORY="${REPO_DIRECTORY}/deploy/operator"
  deploy="${DEPLOYMENT_DIRECTORY}/pmem-csi-operator.yaml"

  echo "Deleting operator components in namespace '${TEST_OPERATOR_NAMESPACE}'"
  # Failures are expected like, as deleting namespace could also delete
  # resources created in that namespace. And when try to delete a resoruce
  # after deleting it's namespace will return NotFound error
  sed -e "s;\(namespace: \)pmem-csi$;\1${TEST_OPERATOR_NAMESPACE};g"  -e "s;\(name: \)pmem-csi$;\1${TEST_OPERATOR_NAMESPACE};g" \
    ${DEPLOY_DIRECTORY}/pmem-csi-operator.yaml | \
    ${KUBECTL} delete -f - 2>&1 | grep -v NotFound || true

  echo "Deleting CRD..."
  ${KUBECTL} delete crd/pmemcsideployments.pmem-csi.intel.com
}

deploy_method=yaml
if [ $# -ge 1 -a "$1" == "-olm" ]; then
  deploy_method=olm
fi

case $deploy_method in
  yaml)
    delete_operator ;;
  olm)
    delete_olm_operator ;;
  *)
    echo >&2 "Unknown deploy method!!!"
    exit 1 ;;
esac

