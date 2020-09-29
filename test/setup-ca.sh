#!/bin/sh

# Directory to use for storing intermediate files.
CA=${CA:="pmem-ca"}
WORKDIR=${WORKDIR:-$(mktemp -d -u -t pmem-XXXX)}
mkdir -p $WORKDIR
cd $WORKDIR

# Check for cfssl utilities.
cfssl_found=1
(command -v cfssl 2>&1 >/dev/null && command -v cfssljson 2>&1 >/dev/null) || cfssl_found=0
if [ $cfssl_found -eq 0 ]; then
    echo "cfssl tools not found, Please install cfssl and cfssljson."
    exit 1
fi

# Generate CA certificates.
<<EOF cfssl gencert -initca - | cfssljson -bare ca
{
    "CN": "$CA",
    "hosts": [ "$CA" ],
    "key": {
        "algo": "rsa",
        "size": 2048
    }
}
EOF

# Generate server and client certificates.
DEFAULT_CNS="pmem-registry pmem-node-controller"
CNS="${DEFAULT_CNS} ${EXTRA_CNS:=""}"
for name in ${CNS}; do
  <<EOF cfssl gencert -ca=ca.pem -ca-key=ca-key.pem - | cfssljson -bare $name
{
    "CN": "$name",
    "hosts": [
        $(if [ "$name" = "pmem-registry" ]; then
             # Some extra names needed for scheduler extender and webhook.
             echo '"pmem-csi-scheduler", "pmem-csi-scheduler.default", "pmem-csi-scheduler.default.svc", "127.0.0.1",'
             # And for metrics server.
             echo '"pmem-csi-metrics", "pmem-csi-metrics.default", "pmem-csi-metrics.default.svc",'
          fi
        )
        "$name"
    ],
    "key": {
        "algo": "ecdsa",
        "size": 256
    }
}
EOF
done
