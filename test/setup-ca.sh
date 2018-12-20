#!/bin/sh

# Directory to use for storing intermediate files
CA=${CA:="pmem-ca"}
WORKDIR=${WORKDIR:-$(mktemp -d -u -t pmem-XXXX)}
mkdir -p $WORKDIR
cd $WORKDIR

#download cfssl utilities
cfssl_found=1
(command -v cfssl 2>&1 >/dev/null && command -v cfssljson 2>&1 >/dev/null) || cfssl_found=0
if [ $cfssl_found -eq 0 ]; then
    echo "cfssl tools not found, Please install cfssl, cfssljson."
    exit 1
fi

#Generate CA certificates
echo '{"CN": "'$CA'", "key": {"algo": "rsa", "size": 2048}}' | cfssl gencert -initca - | cfssljson -bare ca

# Generate server and client certificates
DEFAULT_CNS="pmem-registry pmem-node-controller"
CNS="${DEFAULT_CNS} ${EXTRA_CNS:=""}"
for name in ${CNS}; do
  echo '{"CN": "'$name'", "key": {"algo": "ecdsa", "size": 256}}' | \
  cfssl gencert -ca=ca.pem -ca-key=ca-key.pem - | cfssljson -bare $name
done

echo "Generated certificates can be located at: $WORKDIR"
