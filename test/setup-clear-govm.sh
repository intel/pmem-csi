#!/bin/bash
#
# Implements the first-boot configuration of the different virtual machines
# for Clear Linux running in GoVM.
#
# This script runs *inside* the cluster. All setting env variables
# used by it must be passed in explicitly via ssh.

set -x
set -o errexit
set -o pipefail

HOSTNAME=${HOSTNAME:-$1}
IPADDR=${IPADDR:-127.0.0.1}
BUNDLES="cloud-native-basic containers-basic ${TEST_CLEAR_LINUX_BUNDLES}"

function error_handler(){
    local line="${1}"
    echo  "Running the ${BASH_COMMAND} on function ${FUNCNAME[1]} at line ${line}"
}

function install_kubernetes(){
    trap 'error_handler ${LINENO}' ERR
    # Setup clearlinux environment
    # Disable swupd autoupdate service
    sudo swupd autoupdate --disable

    # Install Kubernetes and additional bundles
    sudo -E swupd bundle-add $BUNDLES
    sudo swupd clean
    sudo mkdir -p /etc/sysctl.d

    # Enable IP Forwarding
    sudo bash -c "echo net.ipv4.ip_forward = 1 >/etc/sysctl.d/60-k8s.conf"
    sudo systemctl restart systemd-sysctl

    # Due to stateless /etc is empty but /etc/hosts is needed by k8s pods.
    # It also expects that the local host name can be resolved. Let's use a nicer one
    # instead of the normal default (clear-<long hex string>).
    sudo bash -c "cat <<EOF >>/etc/hosts
127.0.0.1 localhost
$IPADDR $HOSTNAME
EOF"

    # br_netfilter must be loaded explicitly on the Clear Linux KVM kernel (and only there),
    # otherwise the required /proc/sys/net/bridge/bridge-nf-call-iptables isn't there.
    sudo modprobe br_netfilter
    sudo bash -c "echo br_netfilter >>/etc/modules"

    # Disable swap (permanently).
    swap_var=$(cat /proc/swaps | sed -n -e 's;^/dev/\([0-9a-z]*\).*;dev-\1.swap;p')
    if [ ! -z "$swap" ]; then
        sudo systemctl mask $swap_var
    fi
    sudo swapoff -a

    # We put config changes in place for both runtimes, even though only one of them will
    # be used by Kubernetes, just in case that someone wants to use them manually.

    # Proxy settings for CRI-O.
    sudo mkdir /etc/systemd/system/crio.service.d
    sudo bash -c "cat >/etc/systemd/system/crio.service.d/proxy.conf <<EOF
[Service]
Environment=\"HTTP_PROXY=${HTTP_PROXY}\" \"HTTPS_PROXY=${HTTPS_PROXY}\" \"NO_PROXY=${NO_PROXY}\"
EOF"

    # Testing may involve a Docker registry running on the build host (see
    # TEST_LOCAL_REGISTRY and TEST_PMEM_REGISTRY). We need to trust that
    # registry, otherwise CRI-O will fail to pull images from it.

    sudo mkdir -p /etc/containers
    sudo bash -c "cat >/etc/containers/registries.conf <<EOF
[registries.insecure]
registries = [ $(echo $INSECURE_REGISTRIES | sed 's|^|"|g;s| |", "|g;s|$|"|') ]
EOF"

    # The same for Docker.
    sudo mkdir -p /etc/docker
    sudo bash -c "cat >/etc/docker/daemon.json <<EOF
{ \"insecure-registries\": [ $(echo $INSECURE_REGISTRIES | sed 's|^|"|g;s| |", "|g;s|$|"|') ] }
EOF"

    # Proxy settings for Docker.
    sudo mkdir -p /etc/systemd/system/docker.service.d/
    sudo bash -c "cat >/etc/systemd/system/docker.service.d/proxy.conf <<EOF
[Service]
Environment=\"HTTP_PROXY=$HTTP_PROXY\" \"HTTPS_PROXY=$HTTPS_PROXY\" \"NO_PROXY=$NO_PROXY\"
EOF"

    # Disable the use of Kata containers as default runtime in Docker.
    # The Kubernetes control plan (apiserver, etc.) fails to run otherwise
    # ("Host networking requested, not supported by runtime").

    sudo bash -c "cat >/etc/systemd/system/docker.service.d/51-runtime.conf <<EOF
[Service]
Environment=\"DOCKER_DEFAULT_RUNTIME=--default-runtime runc\"
EOF
"
    case $TEST_CRI in
        docker)
	    # Choose Docker by disabling the use of CRI-O in KUBELET_EXTRA_ARGS.
	    cri_daemon=docker
	    sudo mkdir -p /etc/systemd/system/kubelet.service.d/
	    sudo bash -c "cat >/etc/systemd/system/kubelet.service.d/10-kubeadm.conf <<EOF
[Service]
Environment="KUBELET_EXTRA_ARGS="
EOF"
	    ;;
        crio)
	    # Nothing to do, it is the default in Clear Linux.
	    cri_daemon=cri-o
	    ;;
        *)
	    echo "ERROR: unsupported TEST_CRI=$TEST_CRI"
	    exit 1
	    ;;
    esac

    # flannel + CRI-O + Kata Containers needs a crio.conf change (https://clearlinux.org/documentation/clear-linux/tutorials/kubernetes):
    #    If you are using CRI-O and flannel and you want to use Kata Containers, edit the /etc/crio/crio.conf file to add:
    #    [crio.runtime]
    #    manage_network_ns_lifecycle = true
    #
    # We don't use Kata Containers, so that particular change is not made to /etc/crio/crio.conf
    # at this time.

    # /opt/cni/bin is where runtimes like CRI-O expect CNI plugins. But cloud-native-basic installs into
    # /usr/libexec/cni. Instructions at https://clearlinux.org/documentation/clear-linux/tutorials/kubernetes#id2
    # are inconsistent at this time (https://github.com/clearlinux/clear-linux-documentation/issues/388).
    #
    # We solve this by creating the directory and symlinking all existing CNI plugins into it.
    sudo mkdir -p /opt/cni/bin
    for i in /usr/libexec/cni/*;do
        sudo ln -s $i /opt/cni/bin/
    done

    # Reconfiguration done, start daemons. Starting kubelet must wait until kubeadm has created
    # the necessary config files.
    sudo systemctl daemon-reload
    sudo systemctl restart $cri_daemon
    sudo systemctl enable $cri_daemon kubelet
}

install_kubernetes
