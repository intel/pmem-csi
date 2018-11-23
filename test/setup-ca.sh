#! /bin/sh -e

# The default is to build certstrap from source for each invocation,
# using the source code bundled with OIM, and to put output files
# into _work/ca.
: ${CERTSTRAP:=go run $(pwd)/vendor/github.com/square/certstrap/certstrap.go}

# Default CA name.
: ${CA:=OIM CA}

# Default output directory.
: ${DEPOT_PATH:=_work/ca}

# Check that it works.
${CERTSTRAP} --help >/dev/null

CERTSTRAP="${CERTSTRAP} --depot-path ${DEPOT_PATH}"

# For testing purposes it is okay to not use a passphrase.
${CERTSTRAP} init --common-name "${CA}" --passphrase ""

# These common names have a special meaning in OIM.
DEFAULT_NAMES="user.admin component.registry"

# Here the "normal" part does not matter, it just has to
# be something other than "admin".
DEFAULT_NAMES="${DEFAULT_NAMES} user.normal"

# For each node in the test cluster we need a certificate for the
# caller and for the recipient of commands. "host-0/1/2/..." is what
# we use for the first node and also as controller ID in the example
# deployment of OIM.
DEFAULT_NAMES="${DEFAULT_NAMES} $(for i in $(seq 0 $((NUM_NODES - 1))); do echo host.host-$i controller.host-$i; done)"

for name in ${NAMES:-${DEFAULT_NAMES}}; do
    ${CERTSTRAP} request-cert --common-name "$name" --passphrase ""
    ${CERTSTRAP} sign "$name" --CA "${CA}" --passphrase ""
done

# Now turn all of this into a .yaml file for deployment as "oim-ca" secret.

cat >${DEPOT_PATH}/secret.yaml <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: oim-ca
  namespace: default
type: Opaque
data:
EOF
for name in ${NAMES:-${DEFAULT_NAMES}}; do
    case $name in
        host.*)
            for suffix in crt key; do
                echo "  $name.$suffix: $(base64 -w 0 ${DEPOT_PATH}/$name.$suffix)" >>${DEPOT_PATH}/secret.yaml
            done
            ;;
    esac
done
echo "  ca.crt: $(base64 -w 0 ${DEPOT_PATH}/ca.crt)" >>${DEPOT_PATH}/secret.yaml
