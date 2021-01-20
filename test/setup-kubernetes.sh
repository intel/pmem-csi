#!/bin/bash
#
# The generic part of the Kubernetes cluster setup.
#
# This script runs *inside* the cluster. All setting env variables
# used by it must be passed in explicitly via ssh.

set -x
set -o errexit # TODO: replace with explicit error checking and messages
set -o pipefail

function error_handler(){
        local line="${1}"
        echo >&2 "ERROR: command '${BASH_COMMAND}' in function ${FUNCNAME[1]} at $0:${line} failed"
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

k8sversion=$(kubeadm version -o short)
distro=`egrep "^ID=" /etc/os-release |awk -F= '{print $2}'`
case $distro in
    clear-linux-os)
	# From https://kubernetes.io/docs/setup/independent/create-cluster-kubeadm/#pod-network
	# However, the commit currently listed there for 1.16 is broken. Current master fixes some issues
	# and works.
	podnetworkingurl=https://raw.githubusercontent.com/coreos/flannel/v0.12.0/Documentation/kube-flannel.yml
	# Needed for flannel (https://clearlinux.org/latest/tutorials/kubernetes.html).
	kubeadm_config_cluster="$kubeadm_config_cluster
networking:
  podSubnet: \"10.244.0.0/16\""
	;;
    fedora)
	# Use weave on Fedora. Nothing to add to kubeconfig.
	podnetworkingurl=https://cloud.weave.works/k8s/net?k8s-version=$k8sversion
	;;
    *)
	echo "ERROR: unsupported distro=$distro"
	exit 1
	;;
esac

list_gates () (
    IFS=","
    for f in ${TEST_FEATURE_GATES}; do
        echo "  $f" | sed 's/=/: /g'
    done
)

# We create a scheduler configuration on the master node which enables
# the PMEM-CSI scheduler extender. Because of the "managedResources"
# filter, the extender is only going to be called for pods which
# explicitly enable it and thus other pods (including PMEM-CSI
# itself!)  can be scheduled without it.
#
# In order to reach the scheduler extender, a fixed node port
# is used regardless of the driver name, so only one deployment
# can be active at once. In production this has to be solved
# differently.
#
# Usually the driver name will be "pmem-csi.intel.com", but for testing
# purposed we also configure a second extender.
sudo mkdir -p /var/lib/scheduler/
sudo cp ca.crt /var/lib/scheduler/

case "$k8sversion" in
    v1.1[5678]*)
        # https://github.com/kubernetes/kubernetes/blob/52d7614a8ca5b8aebc45333b6dc8fbf86a5e7ddf/staging/src/k8s.io/kube-scheduler/config/v1alpha1/types.go#L38-L107
        sudo sh -c 'cat >/var/lib/scheduler/scheduler-config.yaml' <<EOF
apiVersion: kubescheduler.config.k8s.io/v1alpha1
kind: KubeSchedulerConfiguration
schedulerName: default-scheduler
algorithmSource:
  policy:
    file:
      path: /var/lib/scheduler/scheduler-policy.cfg
clientConnection:
  # This is where kubeadm puts it.
  kubeconfig: /etc/kubernetes/scheduler.conf
EOF

        # https://github.com/kubernetes/kubernetes/blob/52d7614a8ca5b8aebc45333b6dc8fbf86a5e7ddf/staging/src/k8s.io/kube-scheduler/config/v1/types.go#L28-L47
        sudo sh -c 'cat >/var/lib/scheduler/scheduler-policy.cfg' <<EOF
{
  "kind" : "Policy",
  "apiVersion" : "v1",
  "extenders" :
    [{
      "urlPrefix": "https://127.0.0.1:${TEST_SCHEDULER_EXTENDER_NODE_PORT}",
      "filterVerb": "filter",
      "prioritizeVerb": "prioritize",
      "nodeCacheCapable": true,
      "weight": 1,
      "managedResources":
      [{
        "name": "pmem-csi.intel.com/scheduler",
        "ignoredByScheduler": true
      }]
    },
    {
      "urlPrefix": "https://127.0.0.1:${TEST_SCHEDULER_EXTENDER_NODE_PORT}",
      "filterVerb": "filter",
      "prioritizeVerb": "prioritize",
      "nodeCacheCapable": true,
      "weight": 1,
      "managedResources":
      [{
        "name": "second.pmem-csi.intel.com/scheduler",
        "ignoredByScheduler": true
      }]
    }]
}
EOF
        ;;
    *)
        # https://github.com/kubernetes/kubernetes/blob/1afc53514032a44d091ae4a9f6e092171db9fe10/staging/src/k8s.io/kube-scheduler/config/v1beta1/types.go#L44-L96
        sudo sh -c 'cat >/var/lib/scheduler/scheduler-config.yaml' <<EOF
apiVersion: kubescheduler.config.k8s.io/v1beta1
kind: KubeSchedulerConfiguration
clientConnection:
  # This is where kubeadm puts it.
  kubeconfig: /etc/kubernetes/scheduler.conf
extenders:
- urlPrefix: https://127.0.0.1:${TEST_SCHEDULER_EXTENDER_NODE_PORT}
  filterVerb: filter
  prioritizeVerb: prioritize
  nodeCacheCapable: true
  weight: 1
  managedResources:
  - name: pmem-csi.intel.com/scheduler
    ignoredByScheduler: true
- urlPrefix: https://127.0.0.1:${TEST_SCHEDULER_EXTENDER_NODE_PORT}
  filterVerb: filter
  prioritizeVerb: prioritize
  nodeCacheCapable: true
  weight: 1
  managedResources:
  - name: second.pmem-csi.intel.com/scheduler
    ignoredByScheduler: true
EOF
        ;;
esac


# We always use systemd. Auto-detected for Docker, but not for other
# CRIs
# (https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/install-kubeadm/#configure-cgroup-driver-used-by-kubelet-on-control-plane-node).
kubeadm_config_kubelet="$kubeadm_config_kubelet
cgroupDriver: systemd
"

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
    $(case "$k8sversion" in v1.1[5678]*) : ;; *) echo "runtime-config: storage.k8s.io/v1alpha1";; esac)
controllerManager:
  extraArgs:
    feature-gates: ${TEST_FEATURE_GATES}
    # Let the kube-controller-manager run as fast as it can.
    kube-api-burst: \"100000\"
    kube-api-qps: \"100000\"
scheduler:
  extraVolumes:
    - name: config
      hostPath: /var/lib/scheduler
      mountPath: /var/lib/scheduler
      readOnly: true
    # This is necessary to ensure that the API server accepts
    # certificates signed by the cluster root CA when
    # establishing an https connection to a webhook.
    #
    # This works because the normal trust store
    # of the host (/etc/ssl/certs) is not mounted
    # for the scheduler. If it was (as for the apiserver),
    # then adding another file would either modify
    # the host (for the mount point) or fail (when
    # the bind mount is read-only).
    - name: cluster-root-ca
      hostPath: /var/lib/scheduler/ca.crt
      mountPath: /etc/ssl/certs/ca.crt
      readOnly: true
  extraArgs:
    feature-gates: ${TEST_FEATURE_GATES}
    $(if [ -e /var/lib/scheduler/scheduler-config.yaml ]; then echo 'config: /var/lib/scheduler/scheduler-config.yaml'; fi)
"

if [ -e /dev/vdc ]; then
    # We have an extra volume specifically for etcd (see TEST_ETCD_VOLUME).
    sudo mkdir -p /mnt/etcd-volume
    sudo mkfs.ext4 -F /dev/vdc
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
sudo kubeadm init $kubeadm_args $kubeadm_args_init || (
    set +e
    # Dump some information that might explain the failure.
    sudo systemctl status docker crio containerd kubelet
    sudo journalctl -xe -u docker -u crio -u containerd -u kubelet
    exit 1
)
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

kubectl apply -f $podnetworkingurl

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
fi
