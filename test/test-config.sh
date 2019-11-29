# This file is meant to be sourced into various scripts in this directory and provides
# some common settings.
#
# All of these settings can be overridden by environment variables. This makes
# it possible to create different clusters in parallel with different settings,
# for example:
# TEST_CRI=docker CLUSTER=clear-govm-docker make start
# TEST_CRI=crio CLUSTER=clear-govm-crio make start

# Allow overriding the configuration in additional file(s).
if [ -d test/test-config.d ]; then
    for i in $(ls test/test-config.d/*.sh 2>/dev/null | sort); do
        . $i
    done
fi

# The container runtime that is meant to be used inside Clear Linux.
# Possible values are "docker" and "crio".
#
# Docker is the default for two reasons:
# - survives killing the VMs while cri-o doesn't (https://github.com/kubernetes-sigs/cri-o/issues/1742#issuecomment-442384980)
# - Docker mounts /sys read/write while cri-o read-only. pmem-csi needs it in writable state.
: ${TEST_CRI:=docker}

# A local registry running on the build host, aka localhost:5000.
# In order to reach it from inside the virtual cluster, we need
# to use a public IP address that the registry is likely to listen
# on. Here we default to the IP address of the docker0 interface.
: ${TEST_LOCAL_REGISTRY:=$(ip addr show dev docker0 2>/dev/null | (grep " inet " || echo localhost) | sed -e 's/.* inet //' -e 's;/.*;;'):5000}

# Set up a Docker registry on the master node.
: ${TEST_CREATE_REGISTRY:=false}

# The registry used for PMEM-CSI image(s). Must be reachable from
# inside the cluster. The default is the registry on the master
# node if that is enabled, otherwise a registry on the build
# host.
: ${TEST_PMEM_REGISTRY:=$(if ${TEST_CREATE_REGISTRY}; then echo pmem-csi-${CLUSTER}-master:5000; else echo ${TEST_LOCAL_REGISTRY}; fi)}

# The same registry reachable from the build host.
# This is needed for "make push-images". Pushing
# to the registry on the master node (TEST_CREATE_REGISTRY=true)
# is only supported when making additional changes on the
# build host (like enabling insecure access to that registry)
# and therefore the default is always a local registry.
: ${TEST_BUILD_PMEM_REGISTRY:=localhost:5000}

# Additional insecure registries (for example, my-registry:5000),
# separated by spaces. The default local registry above is always
# marked as insecure and does not need to be listed.
: ${TEST_INSECURE_REGISTRIES:=}

# When using TEST_CREATE_REGISTRY, some images can be copied into
# that registry to boot-strap the cluster.
#
# To use this, do:
# - make build-images
# - TEST_CREATE_REGISTRY=true make start
: ${TEST_BOOTSTRAP_IMAGES:=${TEST_BUILD_PMEM_REGISTRY}/pmem-csi-driver:v0.5.25 ${TEST_BUILD_PMEM_REGISTRY}/pmem-csi-driver-test:v0.5.25}

# Additional Clear Linux bundles.
: ${TEST_CLEAR_LINUX_BUNDLES:=storage-utils}

# Called after Kubernetes has been configured and started on the master node.
: ${TEST_CONFIGURE_POST_MASTER:=}

# Called after Kubernetes has been configured and started on all nodes.
: ${TEST_CONFIGURE_POST_ALL:=}

# PMEM NVDIMM configuration.
#
# See https://github.com/qemu/qemu/blob/bd54b11062c4baa7d2e4efadcf71b8cfd55311fd/docs/nvdimm.txt
# for details about QEMU simulated PMEM.
: ${TEST_MEM_SLOTS:=2}
: ${TEST_NORMAL_MEM_SIZE:=2048} # 2GB
: ${TEST_PMEM_MEM_SIZE:=32768} # 32GB
: ${TEST_PMEM_SHARE:=on}
: ${TEST_PMEM_LABEL_SIZE:=2097152}

# Kubernetes feature gates to enable/disable
# featurename=true,feature=false
: ${TEST_FEATURE_GATES:=CSINodeInfo=true,CSIDriverRegistry=true}

# DeviceMode to be used during testing.
# Allowed values: lvm, direct
# This string is used as part of deployment file name.
: ${TEST_DEVICEMODE:=lvm}

# Which deployment to use during testing.
# Allowed values: testing (default), production
: ${TEST_DEPLOYMENTMODE:=testing}

# Initialize "region0" as required by PMEM-CSI.
: ${TEST_INIT_REGION:=true}

# Validate signature of downloaded image files.
# This may have to be disabled for Clear Linux depending
# on the version of OpenSSL on the build host
# (https://github.com/clearlinux/distribution/issues/85).
: ${TEST_CHECK_SIGNED_FILES:=true}

# If set to a number, that version of Clear Linux is installed
# instead of the latest one.
: ${TEST_CLEAR_LINUX_VERSION:=}
