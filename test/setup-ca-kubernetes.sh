#!/bin/sh -e

# This script generates the certificates needed for securing pmem csi components.
# This is supposed to run prior to deploying pmem-csi driver in a cluster.

# The default relies on a functional kubectl that uses the target cluster.
: ${KUBECTL:=kubectl}

# Directory to use for storing intermediate files
WORKDIR=${WORKDIR:-$(mktemp -d -u -t pmem-XXXX)}

cfssl_found=1
(command -v cfssl 2>&1 >/dev/null && command -v cfssljson 2>&1 >/dev/null) || cfssl_found=0
if [ $cfssl_found -eq 0 ]; then
    echo "cfssl tools not found, Please install cfssl, cfssljson."
    exit 1
fi

# Find the nodes for which we need to create node certificates
NODES=$($KUBECTL get no -l storage=pmem -o name | sed  -e 's;.*/;;')
if [ -z "$NODES" ]; then
    echo "No nodes found in the cluster with 'storage=pmem' label. Probably, you missed labeling of nodes."
    echo "Rerun '$0' script after labeling the cluster nodes that provide persistent memory."
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

mkdir -p $WORKDIR 2> /dev/null && cd $WORKDIR
trap cleaup INT
cleanup()
{
    echo "Cleaning up..."
    cd .. && rm -rf $WORKDIR 2> /dev/null
}

echo "Generating certificate files in: $WORKDIR"

if [ -z "$($KUBECTL get secret pmem-csi-registry-secrets 2> /dev/null)" ]; then
    # Generate PMEM registry server certificate signing request
    generate_csr "pmem-registry"

    $KUBECTL delete secret "pmem-csi-registry-secrets" 2> /dev/null || true
    #store the approved registry certificate and key inside kubernetes secrets
    $KUBECTL create -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
    name: pmem-csi-registry-secrets
type: kubernetes.io/tls
data:
    tls.crt: $(base64 -w 0 pmem-registry.crt)
    tls.key: $(base64 -w 0 pmem-registry-key.pem)
EOF

fi

existing_secrets=
existing_nodes=""
# Get existing node certificates if any
if [ -n "$($KUBECTL get secret pmem-csi-node-secrets 2> /dev/null)" ]; then
    # Retrieve existing node certificates
    existing_secrets=$($KUBECTL get secret pmem-csi-node-secrets -o go-template="'"'{{range $key, $value := .data}}  {{$key}}: {{$value}}{{"\n"}}{{end}}'"'")
    existing_nodes=$(echo "$existing_secrets" | cut -f1 -d':' | sed -e 's;.key;;' -e 's;.crt;;' | uniq)
fi

echo "$existing_secrets"
echo "$existing_nodes"

FINAL_NODES=
for node in $NODES; do
    if [ -z $(echo "$existing_nodes" | grep $node) ]; then
        create_node_secret=1
        FINAL_NODES="$FINAL_NODES $node"
        generate_csr "pmem-node-controller" "$node"
    else
        echo "Secrets for '$node' already found in 'pmem-csi-node-secrets'"
    fi
done

if [ -n "$FINAL_NODES" ]; then
    # Store all node certificates into kubernetes secrets
    echo "Generating node secrets: pmem-csi-node-secrets."
    $KUBECTL delete secret "pmem-csi-node-secrets" 2> /dev/null || true
    $KUBECTL create -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: pmem-csi-node-secrets
type: Opaque
data:
$(echo "$existing_secrets")
$(for name in ${FINAL_NODES}; do
echo "  $name.crt: $(base64 -w 0 $name.crt)"
echo "  $name.key: $(base64 -w 0 $name-key.pem)"
done)
EOF
fi

[ "$(ls -A $WORKDIR)" ] || cleanup
