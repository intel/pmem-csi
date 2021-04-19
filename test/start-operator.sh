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
upgrade=""

function deploy_using_olm() {
  set -x
  # Choose the latest(by creation time) bundle version if not provided one
  BUNDLE_VERSION=${BUNDLE_VERSION:-$(ls -1t ${REPO_DIRECTORY}/deploy/olm-bundle | head -1)}

  echo "Starting the operator using OLM...."
  BUNDLE_DIR="${REPO_DIRECTORY}/deploy/olm-bundle/${BUNDLE_VERSION}"
  BINDIR=${REPO_DIRECTORY}/_work/bin

  if [ ! -d "${BUNDLE_DIR}" ]; then
    echo >&2 "'${BUNDLE_DIR}' not a directory"
    return 1
  fi

  set +e
  output=$(${KUBECTL} get csv -o jsonpath='{.items[*].metadata.name}' 2>&1)
  if [ $? -eq 0 ]; then
    for csv in $output; do
      re="^([^.v]+).v(.*)$"
      [[ "$csv" =~ $re ]] && name=${BASH_REMATCH[1]} && ver=${BASH_REMATCH[2]}
      # if there is a running operator deployment just reuse that
      if [ $name == "pmem-csi-operator" ] ; then
        if [ $ver == "${BUNDLE_VERSION}" ]; then
          echo "Found a running operator deployment with given version!!!"
          exit 0
        fi
        # else treat it as an upgrade/downgrade
        upgrade="-upgrade"
      fi
    done
  fi
  set -e

  tmpdir="$(mktemp -d)"
  TMP_BUNDLE_DIR="${tmpdir}/${BUNDLE_VERSION}"
  trap 'rm -rf $tmpdir' SIGTERM SIGINT EXIT
  # We need to alter generated catalog with custom driver/operator image
  # So copy them to temproary location
  cp -rv ${BUNDLE_DIR} ${tmpdir}/

  # find the latest catalog version
  CSV_FILE="${TMP_BUNDLE_DIR}/manifests/pmem-csi-operator.clusterserviceversion.yaml"

  # Update docker registry
  # Generated CSV's containerImage is 'intel/pmem-csi-driver:v${BUNDLE_VERSION}'
  if [ "${TEST_PMEM_REGISTRY}" != "intel" ]; then
    sed -i -e "s^intel/pmem-csi-driver:^${TEST_PMEM_REGISTRY}/pmem-csi-driver:^g" ${CSV_FILE}
  fi
  if [ "${TEST_PMEM_IMAGE_TAG}" != "" ]; then
    sed -i -e "s^\(/pmem-csi-driver:\).*^\1${TEST_PMEM_IMAGE_TAG}^g" ${CSV_FILE}
  fi
  if [ "${TEST_IMAGE_PULL_POLICY}" != "IfNotPresent" ]; then
    sed -i -e "s^imagePullPolicy:.IfNotPresent^imagePullPolicy: ${TEST_IMAGE_PULL_POLICY}^g" ${CSV_FILE}
  fi
  # Patch operator deployment with appropriate label(pmem-csi.intel.com/deployment=<<deployment-name>>)
  # OLM overwrites the deployment labels but the underneath ReplicaSet carries these labels.
  if [ "${TEST_OPERATOR_DEPLOYMENT_LABEL}" != "" ]; then
    sed -i -r "/labels:$/{N; s|(\n\s+)(.*)|\1pmem-csi.intel.com/deployment: ${TEST_OPERATOR_DEPLOYMENT_LABEL}\1\2| }" ${CSV_FILE}
  fi
  if [ "${TEST_OPERATOR_LOGLEVEL}" != "" ]; then
      sed -i -e "s;-v=.*;-v=${TEST_OPERATOR_LOGLEVEL};g" ${CSV_FILE}
  fi

  BUNDLE_IMAGE=${TEST_LOCAL_REGISTRY}/pmem-csi-bundle:v${BUNDLE_VERSION}
  # Build and push bundle image
  cd $TMP_BUNDLE_DIR && docker build -f bundle.Dockerfile -t ${BUNDLE_IMAGE} . && docker push ${BUNDLE_IMAGE}

  NAMESPACE=""
  if [ "${TEST_OPERATOR_NAMESPACE}" != "" ]; then
    NAMESPACE="--namespace ${TEST_OPERATOR_NAMESPACE}"
  fi

  # Deploy the operator
  ${BINDIR}/operator-sdk run bundle${upgrade} ${NAMESPACE} --timeout 3m ${BUNDLE_IMAGE} --skip-tls
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
  if [ "${TEST_PMEM_IMAGE_TAG}" != "" ]; then
    ${SSH} sed -ie "'s^\(/pmem-csi-driver:\).*^\1${TEST_PMEM_IMAGE_TAG}^g' $tmpdir/operator.yaml"
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

  if [ "${TEST_OPERATOR_LOGLEVEL}" != "" ]; then
    ${SSH} "sed -i -e 's;-v=.*;-v=${TEST_OPERATOR_LOGLEVEL};g' $tmpdir/operator.yaml"
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

if [ "$upgrade" == "" ]; then
  # Set up TLS secrets in the TEST_OPERATOR_NAMESPACE, with the two different prefixes.
  PATH="${REPO_DIRECTORY}/_work/bin:$PATH" TEST_DRIVER_NAMESPACE="${TEST_OPERATOR_NAMESPACE}" TEST_DRIVER_PREFIX=second-pmem-csi-intel-com ${TEST_DIRECTORY}/setup-ca-kubernetes.sh
  PATH="${REPO_DIRECTORY}/_work/bin:$PATH" TEST_DRIVER_NAMESPACE="${TEST_OPERATOR_NAMESPACE}" ${TEST_DIRECTORY}/setup-ca-kubernetes.sh
fi

  cat <<EOF
PMEM-CSI operator is running in '${TEST_OPERATOR_NAMESPACE}' namespace. To try out deploying the pmem-csi driver:
    cat deploy/common/pmem-csi.intel.com_v1beta1_pmemcsideployment_cr.yaml | ${KUBECTL} create -f -
EOF
