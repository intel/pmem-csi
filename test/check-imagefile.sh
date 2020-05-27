#!/bin/bash
#
# Produces an image file for QEMU in a tmp directory, then
# checks that QEMU comes up with a /dev/pmem0 device.

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
}

sshtovm () {
    echo "Running $@ on VM." >&2
    while true; do
        if out=$(ssh $SSH_ARGS ${CLOUD_USER}@${IP} "$@" 2>&1); then
            # Success!
            echo "$out"
            return
        fi
        # SSH access to the VM is unavailable directly after booting and (with Fedora 31)
        # even intermittently when the SSH server restarts. We deal with this by
        # retrying for a while when we get "connection refused" errors.
        if [ "$SECONDS" -gt "$SSH_TIMEOUT" ]; then
            ( set -x;
              govm list
              docker ps
              docker logs govm.$(id -n -u).${GOVM_NAME}
            )
            die "timeout accessing ${IP} through ssh"
        fi
        if ! echo "$out" | grep -q -e "connect to host ${IP}" -e "Permission denied, please try again" -e "Received disconnect from ${IP} .*: Too many authentication failures"; then
            # Some other error, probably in the command itself. Give up.
            echo "$out"
            return 1
        fi
    done
}

result=
test_nvdimm () {
    sshtovm sudo mkdir -p /mnt || die "cannot created /mnt"
    if ! sshtovm sudo mount -odax /dev/pmem0 /mnt; then
        echo
        sshtovm sudo dmesg
        die "cannot mount /dev/pmem0 with -odax"
    fi
    result="fstype=$(sshtovm stat --file-system -c %T /mnt)"
    result+=" partition_size=$(($(sshtovm cat /sys/class/block/pmem0/size) * 512))"
    result+=" block_size=$(sshtovm stat --file-system -c %s /mnt)"
}

create_image
start_vm
test_nvdimm
echo "SUCCESS: $result"
