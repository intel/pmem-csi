# This file is meant to be sourced into various scripts in this directory and provides
# some common settings.

# The container runtime that is meant to be used inside Clear Linux.
# Possible values are "docker" and "crio".
#
# Docker is the default for two reasons:
# - survives killing the VMs while cri-o doesn't (https://github.com/kubernetes-sigs/cri-o/issues/1742#issuecomment-442384980)
# - Docker mounts /sys read/write while cri-o read-only. pmem-csi needs it in writable state.
TEST_CRI=docker

# Prefix for network devices etc.
TEST_PREFIX=pmemcsi

# IPv4 base address. .1 is used for the host, which also acts
# as NAT gateway. Even numbers are for the guests (.2, .4, ...).
TEST_IP_ADDR=192.168.8

# IP addresses of DNS servers to use inside the VMs, separated by spaces.
# The default is to use the ones specified in /etc/resolv.conf, but those
# might not be reachable from inside the VMs (like for example, 127.0.0.53
# from systemd-network).
TEST_DNS_SERVERS=

# Additional Clear Linux bundles.
TEST_CLEAR_LINUX_BUNDLES="storage-utils"

# Post-install command for each virtual machine. Called with the
# current image number (0 to n-1) as parameter.
TEST_CLEAR_LINUX_POST_INSTALL=

# Called after Kubernetes has been configured and started on the master node.
TEST_CONFIGURE_POST_MASTER=

# Called after Kubernetes has been configured and started on all nodes.
TEST_CONFIGURE_POST_ALL=

# PMEM NVDIMM configuration.
#
# When changing PMEM size after booting the VMs already, _work/*.pmem.raw must
# get deleted to re-initialize the PMEM.
#
# See https://github.com/qemu/qemu/blob/bd54b11062c4baa7d2e4efadcf71b8cfd55311fd/docs/nvdimm.txt
# for details about QEMU simulated PMEM.
TEST_MEM_SLOTS=2
TEST_NORMAL_MEM_SIZE=2048 # 2GB
TEST_PMEM_MEM_SIZE=32768 # 32GB
TEST_PMEM_SHARE=on
TEST_PMEM_LABEL_SIZE=2097152

# allow overriding the configuration in additional file(s)
if [ -d test/test-config.d ]; then
    for i in $(ls test/test-config.d/*.sh 2>/dev/null | sort); do
        . $i
    done
fi
