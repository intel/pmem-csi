#!/bin/bash
#
# Produces an image file for QEMU in a tmp directory, then
# checks that QEMU comes up with a /dev/pmem0p1 device.

TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname $(readlink -f $0))}
source ${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}

function die() {
    echo >&2 "ERROR: $@"
    exit 1
}

: ${GOVM_NAME:=pmem-csi-vm}
: ${RESOURCES_DIRECTORY:=$(pwd)/_work/resources}
: ${VM_IMAGE:=${RESOURCES_DIRECTORY}/Fedora-Cloud-Base-30-1.2.x86_64.raw}
: ${EXISTING_VM_FILE:=}
tmp=$(mktemp -d -p $(pwd)/_work check-imagefile.XXXXXX)
: ${VM_FILE:=$tmp/data/${GOVM_NAME}/nvdimm0} # same file as /data/nvdimm0 above for QEMU inside container
: ${GOVM_YAML:=$tmp/govm.yaml}
: ${SSH_KEY:=${RESOURCES_DIRECTORY}/id_rsa}
: ${SSH_PUBLIC_KEY:=${SSH_KEY}.pub}
: ${SLEEP_ON_FAILURE:=false}

if [ "${EXISTING_VM_FILE}" ]; then
    VM_FILE_SIZE=$(stat -c %s "${EXISTING_VM_FILE}")
else
    VM_FILE_SIZE=$((TEST_PMEM_MEM_SIZE * 1024 * 1024))
fi

KVM_CPU_OPTS="${KVM_CPU_OPTS:-\
 -m ${TEST_NORMAL_MEM_SIZE}M,slots=${TEST_MEM_SLOTS},maxmem=$((${TEST_NORMAL_MEM_SIZE} + $(((VM_FILE_SIZE + 1024 * 1024 - 1) / 1024 / 1024)) ))M -smp ${TEST_NUM_CPUS} \
 -cpu ${TEST_QEMU_CPU} \
 -machine pc,accel=kvm,nvdimm=on}"
EXTRA_QEMU_OPTS="${EXTRA_QWEMU_OPTS:-\
 -object memory-backend-file,id=mem1,share=${TEST_PMEM_SHARE},\
mem-path=/data/nvdimm0,size=${VM_FILE_SIZE} \
 -device nvdimm,id=nvdimm1,memdev=mem1 \
}"

SSH_TIMEOUT=120
SSH_ARGS="-oIdentitiesOnly=yes -oStrictHostKeyChecking=no \
        -oUserKnownHostsFile=/dev/null -oLogLevel=error \
        -i ${SSH_KEY}"

# Might be a symbolic link.
ABS_VM_IMAGE=$(readlink --canonicalize-existing "${VM_IMAGE}") || die "cannot find actual file for ${VM_IMAGE}"
VM_IMAGE="${ABS_VM_IMAGE}"

case ${VM_IMAGE} in
    *Fedora*)
        CLOUD_USER=fedora
        EFI=false
        ;;
    *clear*)
        CLOUD_USER=clear
        EFI=true
        ;;
    *)
        die "unknown cloud image ${VM_IMAGE}"
        ;;
esac

atexit () {
    rm -rf "$tmp"
    govm rm "${GOVM_NAME}"
}
trap atexit EXIT

function die() {
    echo >&2 "ERROR: $@"
    if ${SLEEP_ON_FAILURE}; then
        sleep infinity
    fi
    exit 1
}

print_govm_yaml () {
    cat <<EOF
---
vms:
  - name: ${GOVM_NAME}
    image: ${VM_IMAGE}
    cloud: true
    flavor: medium
    workdir: ${tmp}
    sshkey: ${SSH_PUBLIC_KEY}
    efi: ${EFI}
    ContainerEnvVars:
      - |
        KVM_CPU_OPTS=
        ${KVM_CPU_OPTS}
      - |
        EXTRA_QEMU_OPTS=
        ${EXTRA_QEMU_OPTS}
EOF
}

create_image () {
    if [ "${EXISTING_VM_FILE}" ]; then
        local dir=$(dirname ${VM_FILE})
        mkdir -p "$dir" || die "failed to create $dir directory"
        # Try a hardlink first (faster), only copy if necessary.
        ln "${EXISTING_VM_FILE}" "${VM_FILE}" 2>/dev/null || cp "${EXISTING_VM_FILE}" "${VM_FILE}" || die "failed to create ${VM_FILE} from ${EXISTING_VM_FILE}"
        return
    fi
}

start_vm () {
    print_govm_yaml >"${GOVM_YAML}" || die "failed to create ${GOVM_YAML}"
    govm compose -f "${GOVM_YAML}" || die "govm failed"
    IP=$(govm list -f '{{select (filterRegexp . "Name" "^'${GOVM_NAME}'$") "IP"}}')
    echo "Waiting for ssh connectivity on vm with ip $IP"
    while ! ssh $SSH_ARGS ${CLOUD_USER}@${IP} exit 2>/dev/null; do
        if [ "$SECONDS" -gt "$SSH_TIMEOUT" ]; then
            ( set -x;
              govm list
              docker ps
              docker logs govm.$(id -n -u).${GOVM_NAME}
            )
            die "timeout accessing ${ip} through ssh"
        fi
    done
}

result=
test_nvdimm () {
    ssh $SSH_ARGS ${CLOUD_USER}@${IP} sudo mkdir -p /mnt || die "cannot created /mnt"
    if ! ssh $SSH_ARGS ${CLOUD_USER}@${IP} sudo mount -odax /dev/pmem0p1 /mnt; then
        ssh $SSH_ARGS ${CLOUD_USER}@${IP} sudo dmesg
        die "cannot mount /dev/pmem0p1 with -odax"
    fi
    result="fstype=$(ssh $SSH_ARGS ${CLOUD_USER}@${IP} stat --file-system -c %T /mnt)"
    result+=" partition_size=$(($(ssh $SSH_ARGS ${CLOUD_USER}@${IP} cat /sys/class/block/pmem0p1/size) * 512))"
    result+=" partition_start=$(($(ssh $SSH_ARGS ${CLOUD_USER}@${IP} cat /sys/class/block/pmem0p1/start) * 512))"
    result+=" block_size=$(ssh $SSH_ARGS ${CLOUD_USER}@${IP} stat --file-system -c %s /mnt)"
}

create_image
start_vm
test_nvdimm
echo "SUCCESS: $result"
