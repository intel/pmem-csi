#!/bin/sh -e

# This script generates certificates using setup-ca.sh and converts them into
# the Kubernetes secrets that the PMEM-CSI deployments rely upon for
# securing communication between PMEM-CSI components. Existing secrets
# are updated with new certificates when running it again.

# The script needs a functional kubectl that uses the target cluster.
: ${KUBECTL:=kubectl}

# The directory containing setup-ca*.sh.
: ${TEST_DIRECTORY:=$(dirname $(readlink -f $0))}
. ${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}

tmpdir=`mktemp -d`
trap 'rm -r $tmpdir' EXIT

# Generate certificates. They are not going to be needed again and will
# be deleted together with the temp directory. Only the root CA is
# stored in a permanent location.
WORKDIR="$tmpdir" CA="$TEST_CA" NS="${TEST_DRIVER_NAMESPACE}" PREFIX="${TEST_DRIVER_PREFIX}" "$TEST_DIRECTORY/setup-ca.sh"

# This reads a file and encodes it for use in a secret.
read_key () {
    base64 -w 0 "$1"
}

# Read certificate files and turn them into Kubernetes secrets.
#
# The "registry" part in the file and variable names is historic.
# PMEM-CSI < 0.9.0 used that certificate for the node registry
# and webhooks. PMEM-CSI >= 0.9.0 only uses it for the webhooks
# and no longer has such a registry.
#
# -caFile (controller and all nodes)
CA=$(read_key "${TEST_CA}.pem")
# -certFile (controller)
REGISTRY_CERT=$(read_key "$tmpdir/pmem-registry.pem")
# -keyFile (controller)
REGISTRY_KEY=$(read_key "$tmpdir/pmem-registry-key.pem")

${KUBECTL} get ns ${TEST_DRIVER_NAMESPACE} 2>/dev/null >/dev/null || ${KUBECTL} create ns ${TEST_DRIVER_NAMESPACE}

${KUBECTL} apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
    name: ${TEST_DRIVER_PREFIX}-controller-secret
    namespace: ${TEST_DRIVER_NAMESPACE}
type: kubernetes.io/tls
data:
    ca.crt: ${CA}
    tls.crt: ${REGISTRY_CERT}
    tls.key: ${REGISTRY_KEY}
EOF
