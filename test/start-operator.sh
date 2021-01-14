#!/bin/bash
#
# Supports deploying the operator using:
#  - the deployment yaml
#  - the olm-catalog using OLM
# If the operator is already running, return with no error
# to reuse the same operator.
set -o errexit
set -o pipefail

TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname "$(readlink -f "$0")")}
source "${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}"

CLUSTER=${CLUSTER:-pmem-govm}
REPO_DIRECTORY="${REPO_DIRECTORY:-$(dirname "${TEST_DIRECTORY}")}"
CLUSTER_DIRECTORY="${CLUSTER_DIRECTORY:-${REPO_DIRECTORY}/_work/${CLUSTER}}"
SSH="${CLUSTER_DIRECTORY}/ssh.0"
KUBECTL="${SSH} kubectl" # Always use the kubectl installed in the cluster.

function deploy_using_olm() {
  set -x
  echo "Starting the operator using OLM...."
  CATALOG_DIR="${REPO_DIRECTORY}/deploy/olm-catalog"
  BINDIR=${REPO_DIRECTORY}/_work/bin

  if [ ! -d "${CATALOG_DIR}" ]; then
    echo >&2 "'${CATALOG_DIR}' not a directory"
    return 1
  fi

  # if there is a running operator deployment just reuse that
  set +e
  oupput=$(${KUBECTL} get deployment pmem-csi-operator 2>&1)
  if [ $? -eq 0 ]; then
    echo "Found a running operator deployment!!!"
    exit 0
  elif echo $oupput | grep -q -v '(NotFound)' ; then
    echo "Failed to find the existance of an operator deployment\n   $output"
    exit 1
  fi
  set -e

  tmpdir="$(mktemp -d)"
  TMP_CATALOG_DIR="${tmpdir}/olm-catalog"
  trap 'rm -rf $tmpdir' SIGTERM SIGINT EXIT
  # We need to alter generated catalog with custom driver/operator image
  # So copy them to temproary location
  cp -r ${CATALOG_DIR} ${tmpdir}/

  # find the latest catalog version
  VERSION=$(grep 'currentCSV:' ${TMP_CATALOG_DIR}/pmem-csi-operator.package.yaml | sed -r 's|.*v([0-9]+\.[0-9]+\.[0-9]+)$|\1|')
  CSV_FILE="${TMP_CATALOG_DIR}/${VERSION}/pmem-csi-operator.clusterserviceversion.yaml"

  # Update docker registry
  # NOTE: Also updating image version to 'canary' for tests
  if [ "${TEST_PMEM_REGISTRY}" != "" ]; then
    sed -i -e "s^intel/pmem-csi-driver:v${VERSION}^${TEST_PMEM_REGISTRY}/pmem-csi-driver:canary^g" ${CSV_FILE}
  fi
  if [ "${TEST_IMAGE_PULL_POLICY}" != "" ]; then
    sed -i -e "s^imagePullPolicy:.IfNotPresent^imagePullPolicy: ${TEST_IMAGE_PULL_POLICY}^g" ${CSV_FILE}
  fi
  # Patch operator deployment with appropriate label(pmem-csi.intel.com/deployment=<<deployment-name>>)
  # OLM overwrites the deployment labels but the underneath ReplicaSet carries these labels.
  if [ "${TEST_OPERATOR_DEPLOYMENT_LABEL}" != "" ]; then
    sed -i -r "/labels:$/{N; s|(\n\s+)(.*)|\1pmem-csi.intel.com/deployment: ${TEST_OPERATOR_DEPLOYMENT_LABEL}\1\2| }" ${CSV_FILE}
  fi

  NAMESPACE=""
  if [ "${TEST_OPERATOR_NAMESPACE}" != "" ]; then
    NAMESPACE="--namespace ${TEST_OPERATOR_NAMESPACE}"
  fi

  # Deploy the operator
  ${BINDIR}/operator-sdk run packagemanifests --version ${VERSION} ${NAMESPACE} --install-mode OwnNamespace --timeout 3m ${TMP_CATALOG_DIR}
}

function deploy_using_yaml() {
  crd=${REPO_DIRECTORY}/deploy/crd/pmem-csi.intel.com_pmemcsideployments.yaml
  echo "Deploying '${crd}'..."
  sed -e "s;\(namespace: \)pmem-csi$;\1${TEST_OPERATOR_NAMESPACE};g" ${crd} | ${SSH} kubectl apply -f -

  DEPLOY_DIRECTORY="${REPO_DIRECTORY}/deploy"
  deploy="${DEPLOY_DIRECTORY}/operator/pmem-csi-operator.yaml"
  echo "Deploying '${deploy}'..."

  if [ ! -f "$deploy" ]; then
    echo >&2 "'${deploy}' not a yaml file"
    return 1
  fi
  tmpdir=$(${SSH} mktemp -d)
  trap '${SSH} "rm -rf $tmpdir"' SIGTERM SIGINT EXIT

  ${SSH} "cat > $tmpdir/operator.yaml" <<EOF
$(cat "${deploy}")
EOF

  if [ "${TEST_PMEM_REGISTRY}" != "" ]; then
     ${SSH} sed -ie "s^intel/pmem^${TEST_PMEM_REGISTRY}/pmem^g" "$tmpdir/operator.yaml"
  fi
  if [ "${TEST_IMAGE_PULL_POLICY}" != "" ]; then
    ${SSH} "sed -ie 's;\(imagePullPolicy: \).*;\1${TEST_IMAGE_PULL_POLICY};g' $tmpdir/operator.yaml"
  fi
  if [ "${TEST_OPERATOR_NAMESPACE}" != "" ]; then
    # replace namespace object name
    ${SSH} "sed -ie 's;\(name: \)pmem-csi$;\1${TEST_OPERATOR_NAMESPACE};g' $tmpdir/operator.yaml"
    # replace namespace of other objects
    ${SSH} "sed -ie 's;\(namespace: \)pmem-csi$;\1${TEST_OPERATOR_NAMESPACE};g' $tmpdir/operator.yaml"
    # replace webservice secret dns names
    ${SSH} "sed -ie 's;pmem-csi.svc;${TEST_OPERATOR_NAMESPACE}.svc;g' $tmpdir/operator.yaml"
  fi

  ${SSH} "cat > $tmpdir/kustomization.yaml" <<EOF
resources:
- operator.yaml
EOF

  if [ "${TEST_OPERATOR_DEPLOYMENT_LABEL}" != "" ]; then
  ${SSH} "cat >>'$tmpdir/kustomization.yaml'" <<EOF
commonLabels:
  pmem-csi.intel.com/deployment: ${TEST_OPERATOR_DEPLOYMENT_LABEL}
EOF
  fi

  echo "Deploying the operator in '${TEST_OPERATOR_NAMESPACE}' namespace..."

  ${KUBECTL} apply --kustomize "$tmpdir"
}

deploy_method=yaml
if [ $# -ge 1 -a "$1" == "-olm" ]; then
  deploy_method=olm
fi

case $deploy_method in
  yaml)
    deploy_using_yaml ;;
  olm)
    deploy_using_olm ;;
  *)
    echo >&2 "Unknown deploy method!!!"
    exit 1 ;;
esac

  cat <<EOF
PMEM-CSI operator is running in '${TEST_OPERATOR_NAMESPACE}' namespace. To try out deploying the pmem-csi driver:
    cat deploy/common/pmem-csi.intel.com_v1beta1_pmemcsideployment_cr.yaml | ${KUBECTL} create -f -
EOF
