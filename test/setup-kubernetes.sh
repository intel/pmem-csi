#!/bin/bash
#
# The generic part of the Kubernetes cluster setup.
#
# This script runs *inside* the cluster. All setting env variables
# used by it must be passed in explicitly via ssh.

set -x
set -o errexit # TODO: replace with explicit error checking and messages
set -o pipefail

: ${TEST_CREATE_REGISTRY:=false}

function error_handler(){
        local line="${1}"
        echo >&2 "ERROR: command '${BASH_COMMAND}' in function ${FUNCNAME[1]} at $0:${line} failed"
}

function create_local_registry(){
trap 'error_handler ${LINENO}' ERR
sudo docker run -d -p 5000:5000 --restart=always --name registry registry:2
}

function setup_kubernetes_master(){
trap 'error_handler ${LINENO}' ERR
kubeadm_args=
kubeadm_args_init=
kubeadm_config_init="apiVersion: kubeadm.k8s.io/v1beta1
kind: InitConfiguration"
kubeadm_config_cluster="apiVersion: kubeadm.k8s.io/v1beta1
kind: ClusterConfiguration"
kubeadm_config_kubelet="apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration"
kubeadm_config_proxy="apiVersion: kubeproxy.config.k8s.io/v1alpha1
kind: KubeProxyConfiguration"
kubeadm_config_file="/tmp/kubeadm-config.yaml"

case $TEST_CRI in
    docker)
	# [ERROR SystemVerification]: unsupported docker version: 18.06.1
	kubeadm_args="$kubeadm_args --ignore-preflight-errors=SystemVerification"
	;;
    crio)
	# Needed for CRI-O (https://clearlinux.org/documentation/clear-linux/tutorials/kubernetes).
	kubeadm_config_init="$kubeadm_config_init
nodeRegistration:
  criSocket: /run/crio/crio.sock"
	;;
    containerd)
	;;
    *)
	echo "ERROR: unsupported TEST_CRI=$TEST_CRI"
	exit 1
	;;
esac

# Needed for flannel (https://clearlinux.org/documentation/clear-linux/tutorials/kubernetes).
kubeadm_config_cluster="$kubeadm_config_cluster
networking:
  podSubnet: \"10.244.0.0/16\""

list_gates () (
    IFS=","
    for f in ${TEST_FEATURE_GATES}; do
        echo "  $f" | sed 's/=/: /g'
    done
)

if [ ! -z ${TEST_FEATURE_GATES} ]; then
    kubeadm_config_kubelet="$kubeadm_config_kubelet
featureGates:
$(list_gates)"
    kubeadm_config_proxy="$kubeadm_config_proxy
featureGates:
$(list_gates)"
    kubeadm_config_cluster="$kubeadm_config_cluster
apiServer:
  extraArgs:
    feature-gates: ${TEST_FEATURE_GATES}
controllerManager:
  extraArgs:
    feature-gates: ${TEST_FEATURE_GATES}
scheduler:
  extraArgs:
    feature-gates: ${TEST_FEATURE_GATES}"
fi

if [ -e /dev/vdc ]; then
    # We have an extra volume specifically for etcd (see TEST_ETCD_VOLUME_SIZE).
    sudo mkdir -p /mnt/etcd-volume
    sudo mkfs.ext4 /dev/vdc
    sudo mount /dev/vdc /mnt/etcd-volume
    # etcd wants an empty directory, giving it the volume fails due to "lost+found".
    sudo mkdir /mnt/etcd-volume/etcd
    sudo chmod a+rwx /mnt/etcd-volume/etcd
    kubeadm_config_cluster="$kubeadm_config_cluster
etcd:
  local:
    dataDir: /mnt/etcd-volume/etcd
"
fi

# Use a fixed version of Kubernetes for reproducability. The version gets
# chosen when installing kubeadm. Here we use exactly that version.
k8sversion=$(kubeadm version | sed -e 's/.*GitVersion:"*\([^ ",]*\).*/\1/')
kubeadm_config_cluster="$kubeadm_config_cluster
kubernetesVersion: $k8sversion
"

# TODO: it is possible to set up each node in parallel, see
# https://kubernetes.io/docs/reference/setup-tools/kubeadm/kubeadm-init/#automating-kubeadm


cat >${kubeadm_config_file} <<EOF
$kubeadm_config_init
---
$kubeadm_config_kubelet
---
$kubeadm_config_cluster
---
$kubeadm_config_proxy
EOF

echo "kubeadm config:"
cat ${kubeadm_config_file}

# We install old Kubernetes releases on current distros and must
# disable the kernel preflight check for that to work, because those
# old releases do not necessarily have a recent kernel in their
# whitelist (for example, 1.13.9 fails on Linux
# 5.0.9-301.fc30.x86_64).
kubeadm_args="$kubeadm_args --ignore-preflight-errors=SystemVerification"

kubeadm_args_init="$kubeadm_args_init --config=$kubeadm_config_file"
sudo kubeadm init $kubeadm_args $kubeadm_args_init
mkdir -p $HOME/.kube
sudo cp -i /etc/kubernetes/admin.conf $HOME/.kube/config
sudo chown $(id -u):$(id -g) $HOME/.kube/config


# Verify that Kubernetes works by starting it and then listing pods.
# We also wait for the node to become ready, which can take a while because
# images might still need to be pulled. This can take minutes, therefore we sleep
# for one minute between output.
echo "Waiting for Kubernetes cluster to become ready..."
while ! kubectl get nodes | grep -q 'Ready'; do
        kubectl get nodes
        kubectl get pods --all-namespaces
        sleep 1
done
kubectl get nodes
kubectl get pods --all-namespaces

${TEST_CONFIGURE_POST_MASTER}

# From https://kubernetes.io/docs/setup/independent/create-cluster-kubeadm/#pod-network
# However, the commit currently listed there for 1.16 is broken. Current master fixes some issues
# and works.
kubectl apply -f https://raw.githubusercontent.com/coreos/flannel/2140ac876ef134e0ed5af15c65e414cf26827915/Documentation/kube-flannel.yml

# Install addon storage CRDs, needed if certain feature gates are enabled.
# Only applicable to Kubernetes 1.13 and older. 1.14 will have them as builtin APIs.
if kubectl version | grep -q '^Server Version.*Major:"1", Minor:"1[01234]"'; then
    if [[ "$TEST_FEATURE_GATES" == *"CSINodeInfo=true"* ]]; then
        kubectl create -f https://raw.githubusercontent.com/kubernetes/kubernetes/release-1.13/cluster/addons/storage-crds/csidriver.yaml
    fi
    if [[ "$TEST_FEATURE_GATES" == *"CSIDriverRegistry=true"* ]]; then
        kubectl create -f https://raw.githubusercontent.com/kubernetes/kubernetes/release-1.13/cluster/addons/storage-crds/csinodeinfo.yaml
    fi
fi

# Run additional commands specified in config.
${TEST_CONFIGURE_POST_ALL}

}

if [[ "$HOSTNAME" == *"master"* ]]; then
	setup_kubernetes_master
    if $TEST_CREATE_REGISTRY; then
	    create_local_registry
    fi
fi
