#!/bin/sh -e

# This script generates the certificates needed for securing pmem csi components.
# This is supposed to run prior to deploying pmem-csi driver in a cluster.

# The default relies on a functional kubectl that uses the target cluster.
: ${KUBECTL:=kubectl}

# Directory to use for storing intermediate files
WORKDIR=${WORKDIR:-$(mktemp -d -u -t pmem-XXXX)}
mkdir -p $WORKDIR
cd $WORKDIR

cfssl_found=1
(command -v cfssl 2>&1 >/dev/null && command -v cfssljson 2>&1 >/dev/null) || cfssl_found=0
if [ $cfssl_found -eq 0 ]; then
    echo "cfssl tools not found, Please install cfssl, cfssljson."
    exit 1
fi

generate_csr()
{
    CN=$1
    CSR_NAME=${2:-$CN}

    echo "Generating signing request certificate for '$CN'"
    cert_json=$(echo '{ "CN":  "'$CN'", "key": { "algo": "ecdsa", "size": 256 } }')

    echo $cert_json | cfssl genkey - | cfssljson -bare $CSR_NAME

    echo "Creating kubernetes signing request: $CSR_NAME-csr"
    $KUBECTL delete csr $CSR_NAME-csr 2> /dev/null || true
    cat <<EOF | $KUBECTL create -f -
apiVersion: certificates.k8s.io/v1beta1
kind: CertificateSigningRequest
metadata:
  name: $CSR_NAME-csr
spec:
  groups:
  - system:authenticated
  request: $(cat $CSR_NAME.csr | base64 | tr -d '\n')
  usages:
  - server auth
  - client auth
EOF

    echo "Approving signing request..."
    $KUBECTL certificate approve $CSR_NAME-csr

    # Retrieve certificate. Might take a while before it is ready.
    echo "Waiting for signed certificate..."
    rm -f $CSR_NAME.crt
    while ! [ -s $CSR_NAME.crt ]; do
        sleep 1
        $KUBECTL get csr $CSR_NAME-csr -o jsonpath='{.status.certificate}' | base64 --decode > $CSR_NAME.crt
    done
    echo "Created: $CSR_NAME.crt"
}

echo "Generating certificates: $WORKDIR"

# Generate PMEM registry server certificate signing request
generate_csr "pmem-registry"

$KUBECTL delete secret "pmem-registry-secrets" 2> /dev/null || true
#store the approved registry certificate and key inside kubernetes secrets
$KUBECTL create -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
    name: pmem-registry-secrets
type: kubernetes.io/tls
data:
    tls.crt: $(base64 -w 0 pmem-registry.crt)
    tls.key: $(base64 -w 0 pmem-registry-key.pem)
EOF

# Find the nodes for which we need to create node certificates
NODES=$($KUBECTL get no -l storage=pmem -o name | sed  -e 's;.*/;;')
for node in $NODES; do
    generate_csr "pmem-node-controller" "$node"
done

# Store all node certificates into kubernetes secrets
echo "Generating node secrets: pmem-node-secrets."
$KUBECTL delete secret "pmem-node-secrets" 2> /dev/null || true
$KUBECTL create -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: pmem-node-secrets
type: Opaque
data:
$(for name in ${NODES}; do
    echo "  $name.crt: $(base64 -w 0 $name.crt)"
    echo "  $name.key: $(base64 -w 0 $name-key.pem)"
done)
EOF
