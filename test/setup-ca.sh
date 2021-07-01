#!/bin/sh

# Directory to use for storing intermediate files.
WORKDIR=$(realpath ${WORKDIR:-$(mktemp -d -u -t pmem-XXXX)})
mkdir -p $WORKDIR
cd $WORKDIR
CA=${CA:="$WORKDIR/ca"}
NS=${NS:-pmem-csi}
PREFIX=${PREFIX:-pmem-csi-intel-com}

# Check for cfssl utilities.
cfssl_found=1
(command -v cfssl 2>&1 >/dev/null && command -v cfssljson 2>&1 >/dev/null) || cfssl_found=0
if [ $cfssl_found -eq 0 ]; then
    echo "cfssl tools not found, Please install cfssl and cfssljson."
    exit 1
fi

CADIR=$(dirname ${CA})
mkdir -p "${CADIR}"
CA_CRT=$(realpath ${CA}.pem)
CA_KEY=$(realpath ${CA}-key.pem)
if ! [ -f ${CA_CRT} -a -f ${CA_KEY} ]; then
  echo "Generating CA certificate in $CADIR ..."
  (cd "$CADIR" &&
  <<EOF cfssl gencert -initca - | cfssljson -bare $(basename $CA)
{
    "CN": "pmem-ca",
    "hosts": [ "pmem-ca" ],
    "key": {
        "algo": "rsa",
        "size": 2048
    }
}
EOF
)
fi

# Generate server and client certificates.
DEFAULT_CNS="pmem-controller"
CNS="${DEFAULT_CNS} ${EXTRA_CNS:=""}"
for name in ${CNS}; do
  echo "Generating Certificate for '$name'(NS=$NS) ..."
  tee /dev/stderr <<EOF | cfssl gencert -ca=${CA_CRT} -ca-key=${CA_KEY} - | cfssljson -bare $name
{
    "CN": "$name",
    "hosts": [
        $(if [ "$name" = "pmem-controller" ]; then
             # Some extra names needed for scheduler extender and webhook.
             # The version without intel-com was used by PMEM-CSI < 0.9.0,
             # the version starting with 0.9.0 for the sake of consistency with
             # the pmem-csi.intel.com driver name.
             echo '"127.0.0.1",'
             # mutating pod webhook
             echo '"pmem-csi-webhook", "pmem-csi-webhook.'$NS'", "pmem-csi-webhook.'$NS'.svc",'
             echo '"'$PREFIX'-webhook", "'$PREFIX'-webhook.'$NS'", "'$PREFIX'-webhook.'$NS'.svc",'
             # scheduler extender
             echo '"pmem-csi-scheduler", "pmem-csi-scheduler.'$NS'", "pmem-csi-scheduler.'$NS'.svc",'
             echo '"'$PREFIX'-scheduler", "'$PREFIX'-scheduler.'$NS'", "'$PREFIX'-scheduler.'$NS'.svc",'
             # And for metrics server.
             echo '"pmem-csi-metrics", "pmem-csi-metrics.'$NS'", "pmem-csi-metrics.'$NS'.svc",'
             echo '"'$PREFIX'-metrics", "'$PREFIX'-metrics.'$NS'", "'$PREFIX'-metrics.'$NS'.svc",'
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
