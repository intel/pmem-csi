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

# The operating system to install inside the nodes.
: ${TEST_DISTRO:=clear}

# Choose the version of the operating system that gets installed. Valid
# values depend on the OS.
: ${TEST_DISTRO_VERSION:=}

# The container runtime that is meant to be used.
# Possible values are "docker", "containerd", and "crio". Non-default
# values are untested and may or may not work.
#
# cri-o is the default on Clear Linux because that is supported better
# and Docker elsewhere because we can install it easily.
: ${TEST_CRI:=$(case ${TEST_DISTRO} in clear) echo crio;; *) echo docker;; esac)}

# A local registry running on the build host, aka localhost:5000.
# In order to reach it from inside the virtual cluster, we need
# to use a public IP address that the registry is likely to listen
# on. Here we default to the IP address of the docker0 interface.
: ${TEST_LOCAL_REGISTRY:=$(ip addr show dev docker0 2>/dev/null | (grep " inet " || echo localhost) | sed -e 's/.* inet //' -e 's;/.*;;'):5000}

# The registry used for PMEM-CSI image(s). Must be reachable from
# inside the cluster.
: ${TEST_PMEM_REGISTRY:=${TEST_LOCAL_REGISTRY}}

# The same registry reachable from the build host.
# This is needed for "make push-images".
: ${TEST_BUILD_PMEM_REGISTRY:=localhost:5000}

# Additional insecure registries (for example, my-registry:5000),
# separated by spaces. The default local registry above is always
# marked as insecure and does not need to be listed.
: ${TEST_INSECURE_REGISTRIES:=}

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
: ${TEST_PMEM_MEM_SIZE:=65536} # 64GB
: ${TEST_PMEM_SHARE:=on}
: ${TEST_PMEM_LABEL_SIZE:=2097152}

# Number of CPUS in QEMU VM. Must be at least 2 for Kubernetes.
: ${TEST_NUM_CPUS:=2}

# QEMU -cpu parameter.
#
# "host" enables nested virtualization (required for Kata Containers).
# The build host must have the kvm_intel module loaded with
# nested=1 (see https://wiki.archlinux.org/index.php/KVM#Nested_virtualization).
: ${TEST_QEMU_CPU:=host}

# The etcd instance running on the master node can be configured to
# store its data on a separate disk. This is the path to an existing
# file of the desired size which will then be passed into the master
# node via "-drive file=...". For that to work the file has
# to be inside the "data" directory of the master node.
#
# This is useful when the _work directory is on a slow disk
# because that can lead to slow performance and failures
# (https://github.com/kubernetes/kubernetes/issues/70082).
: ${TEST_ETCD_VOLUME:=}

# Kubernetes feature gates to enable/disable
# featurename=true,feature=false
: ${TEST_FEATURE_GATES:=CSINodeInfo=true,CSIDriverRegistry=true,CSIBlockVolume=true,CSIInlineVolume=true}

# Device mode that test/setup-deployment.sh is using.
# Allowed values: lvm, direct
# This string is used as part of deployment file name.
: ${TEST_DEVICEMODE:=lvm}

# Which deployment test/setup-deployment.sh is using.
# Allowed values: testing (default), production
: ${TEST_DEPLOYMENTMODE:=testing}

# TLS certificates installed by test/setup-deployment.sh.
: ${TEST_CA:=_work/pmem-ca/ca.pem}
: ${TEST_REGISTRY_CERT:=_work/pmem-ca/pmem-registry.pem}
: ${TEST_REGISTRY_KEY:=_work/pmem-ca/pmem-registry-key.pem}
: ${TEST_NODE_CERT:=_work/pmem-ca/pmem-node-controller.pem}
: ${TEST_NODE_KEY:=_work/pmem-ca/pmem-node-controller-key.pem}

# Initialize "region0" as required by PMEM-CSI.
: ${TEST_INIT_REGION:=true}

# Validate signature of downloaded image files.
# This may have to be disabled for Clear Linux depending
# on the version of OpenSSL on the build host
# (https://github.com/clearlinux/distribution/issues/85).
: ${TEST_CHECK_SIGNED_FILES:=true}

# "make start" tests that /dev/kvm exists before invoking govm because
# when it is missing, the failure of QEMU inside the containers is
# hard to diagnose.
#
# However, in some rather special circumstances it may be necessary to
# disable this check. For example, the CI runs "make start" in a
# non-privileged container without /dev/kvm whereas QEMU will run in
# privileged containers where /dev/kvm is available.
: ${TEST_CHECK_KVM:=true}

# If set to a <major>.<minor> number, that version of Kubernetes
# is installed instead of the latest one. Ignored when
# using Clear Linux as OS because with Clear Linux we have
# to use the Kubernetes version that ships with it.
: ${TEST_KUBERNETES_VERSION:=1.16}

# Kubernetes node port number
# (https://kubernetes.io/docs/concepts/services-networking/service/#nodeport)
# that is going to be used by kube-scheduler to reach the scheduler
# extender service (see deploy/scheduler/scheduler-service.yaml).
: ${TEST_SCHEDULER_EXTENDER_NODE_PORT:=32000}
