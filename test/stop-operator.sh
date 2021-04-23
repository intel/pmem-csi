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

keep_crd=false
keep_namespace=false

function delete_olm_operator() {
  BINDIR=${REPO_DIRECTORY}/_work/bin
 
  namespace=""
  if [ "${TEST_OPERATOR_NAMESPACE}" != "" ]; then
    namespace="--namespace ${TEST_OPERATOR_NAMESPACE}"
  fi

  echo "Cleaning up the operator deployment using OLM"
  output=$(${BINDIR}/operator-sdk cleanup pmem-csi-operator $namespace 2>&1)
  if [ $? -ne 0 ] ; then
    echo "Failed to delete the operator: $output"
    exit 1
  fi

  timeout=180 # 3 minutes
  SECONDS=0
  echo "Waiting for all the operator bundle objects gets deleted..."
  while true ; do
    output=$(${KUBECTL} get subscriptions,catalogsource,installplans,csvs,po $namespace 2>&1 | grep -v "No rsources found" || true)
    if ! echo $output | grep -q -E 'pmem-csi-(operator|bundle)' ; then
      echo "Done"
      return
    fi
    if [ $SECONDS -gt $timeout ]; then
      echo "Remove objects timedout: $output"
      exit 1
    fi
    sleep 1
  done
}

function delete_operator() {
  tmpdir=$(mktemp -d)
  trap "rm -rf $tmpdir" SIGTERM SIGINT EXIT

  cp "${REPO_DIRECTORY}/deploy/operator/pmem-csi-operator.yaml" ${tmpdir}/pmem-csi-operator.yaml
  cat > ${tmpdir}/kustomization.yaml <<EOF
resources:
- pmem-csi-operator.yaml
$($keep_namespace && echo -n \
'patchesStrategicMerge:
- patch.yaml'
)
EOF

  if $keep_namespace ; then
    cat > ${tmpdir}/patch.yaml <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: ${TEST_OPERATOR_NAMESPACE}
\$patch: delete
EOF
  fi

  # Failures are expected like, as deleting namespace could also delete
  # resources created in that namespace. And when try to delete a resoruce
  # after deleting it's namespace will return NotFound error
  sed -i -e "s;\(namespace: \)pmem-csi$;\1${TEST_OPERATOR_NAMESPACE};g" \
         -e "s;\(name: \)pmem-csi$;\1${TEST_OPERATOR_NAMESPACE};g" ${tmpdir}/pmem-csi-operator.yaml

  echo "Deleting operator components in namespace '${TEST_OPERATOR_NAMESPACE}'"
  ${REPO_DIRECTORY}/_work/kustomize build $tmpdir | ${KUBECTL} delete -f - 2>&1 | grep -v NotFound || true

  if ! $keep_crd ; then
    echo "Deleting CRD..."
    ${KUBECTL} delete crd/pmemcsideployments.pmem-csi.intel.com 2>&1 | grep -v NotFound || true
  fi
}

function Usage() {
  echo "Usage:
  $0 [-olm] [-keep-namespace] [-keep-crd]"
  exit
}

deploy_method=yaml

for arg in $@; do
  case $arg in
  "-olm") deploy_method=olm ;;
  "-keep-crd") keep_crd=true ;;
  "-keep-namespace") keep_namespace=true ;;
  "-h") Usage ;;
  *) echo "Ignoring unknown argument: $arg"
     Usage ;;
  esac
done

case $deploy_method in
  yaml)
    delete_operator;;
  olm)
    delete_olm_operator;;
  *)
    echo >&2 "Unknown deploy method!!!"
    exit 1 ;;
esac
