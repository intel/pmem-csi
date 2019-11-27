#!/bin/bash
#
# Implements the first-boot configuration of the different virtual machines
# for Clear Linux running in GoVM.
#
# This script runs *inside* the cluster. All setting env variables
# used by it must be passed in explicitly via ssh and it must run as root.

set -x
set -o errexit # TODO: replace with explicit error checking and error messages.
set -o pipefail

: ${INIT_KUBERNETES:=true}
HOSTNAME=${HOSTNAME:-$1}
IPADDR=${IPADDR:-127.0.0.1}
BUNDLES=" ${TEST_CLEAR_LINUX_BUNDLES}"
if ${INIT_KUBERNETES}; then
    BUNDLES="${BUNDLES} cloud-native-basic containers-basic"
fi

function error_handler(){
    local line="${1}"
    echo >&2 "ERROR: the command '${BASH_COMMAND}' at $0:${line} failed"
}
trap 'error_handler ${LINENO}' ERR

function install_bundles(){
    # Setup clearlinux environment
    # Disable swupd autoupdate service
    swupd autoupdate --disable

    # Install Kubernetes and additional bundles
    swupd bundle-add $BUNDLES
    swupd clean
    mkdir -p /etc/sysctl.d

    # Enable IP Forwarding
    echo net.ipv4.ip_forward = 1 >/etc/sysctl.d/60-k8s.conf
    systemctl restart systemd-sysctl

    # Due to stateless /etc is empty but /etc/hosts is needed by k8s pods.
    # It also expects that the local host name can be resolved. Let's use a nicer one
    # instead of the normal default (clear-<long hex string>).
    cat <<EOF >>/etc/hosts
127.0.0.1 localhost
$IPADDR $HOSTNAME
EOF

    # br_netfilter must be loaded explicitly on the Clear Linux KVM kernel (and only there),
    # otherwise the required /proc/sys/net/bridge/bridge-nf-call-iptables isn't there.
    modprobe br_netfilter
    echo br_netfilter >>/etc/modules

    # Disable swap (permanently).
    swap_var=$(cat /proc/swaps | sed -n -e 's;^/dev/\([0-9a-z]*\).*;dev-\1.swap;p')
    if [ ! -z "$swap" ]; then
        systemctl mask $swap_var
    fi
    swapoff -a

    if ${INIT_KUBERNETES}; then
        # We put config changes in place for both runtimes, even though only one of them will
        # be used by Kubernetes, just in case that someone wants to use them manually.

        # Proxy settings for CRI-O.
        mkdir /etc/systemd/system/crio.service.d
        cat >/etc/systemd/system/crio.service.d/proxy.conf <<EOF
[Service]
Environment="HTTP_PROXY=${HTTP_PROXY}" "HTTPS_PROXY=${HTTPS_PROXY}" "NO_PROXY=${NO_PROXY}"
EOF

        # Testing may involve a Docker registry running on the build host (see
        # TEST_LOCAL_REGISTRY and TEST_PMEM_REGISTRY). We need to trust that
        # registry, otherwise CRI-O will fail to pull images from it.

        mkdir -p /etc/containers
        cat >/etc/containers/registries.conf <<EOF
[registries.insecure]
registries = [ $(echo $INSECURE_REGISTRIES | sed 's|^|"|g;s| |", "|g;s|$|"|') ]
EOF

        # The same for Docker.
        mkdir -p /etc/docker
        cat >/etc/docker/daemon.json <<EOF
{ "insecure-registries": [ $(echo $INSECURE_REGISTRIES | sed 's|^|"|g;s| |", "|g;s|$|"|') ] }
EOF

        # Proxy settings for Docker.
        mkdir -p /etc/systemd/system/docker.service.d/
        cat >/etc/systemd/system/docker.service.d/proxy.conf <<EOF
[Service]
Environment="HTTP_PROXY=$HTTP_PROXY" "HTTPS_PROXY=$HTTPS_PROXY" "NO_PROXY=$NO_PROXY"
EOF

        # Disable the use of Kata containers as default runtime in Docker.
        # The Kubernetes control plan (apiserver, etc.) fails to run otherwise
        # ("Host networking requested, not supported by runtime").

        cat >/etc/systemd/system/docker.service.d/51-runtime.conf <<EOF
[Service]
Environment="DOCKER_DEFAULT_RUNTIME=--default-runtime runc"
EOF
    
        mkdir -p /etc/systemd/system/kubelet.service.d/
        case $TEST_CRI in
            docker)
	        cri_daemon=docker
	        # Choose Docker by disabling the use of CRI-O in KUBELET_EXTRA_ARGS.
	        cat >/etc/systemd/system/kubelet.service.d/10-kubeadm.conf <<EOF
[Service]
Environment="KUBELET_EXTRA_ARGS="
EOF

                # Docker depends on containerd, in some Clear Linux
                # releases. Here we assume that it does when it got
                # installed together with Docker and then add the same
                # runtime dependency as for kubelet -> Docker
                # (https://github.com/clearlinux/distribution/issues/1004).
                if [ -f /usr/lib/systemd/system/containerd.service ]; then
                    containerd_daemon=containerd
                    mkdir -p /etc/systemd/system/docker.service.d/
                    cat >/etc/systemd/system/docker.service.d/10-containerd.conf <<EOF
[Unit]
After=containerd.service
EOF
                fi
	        ;;
            crio)
	        cri_daemon=cri-o
	        ;;
            *)
	        echo "ERROR: unsupported TEST_CRI=$TEST_CRI"
	        exit 1
	        ;;
        esac

        # kubelet must start after the container runtime that it depends on.
        # This is not currently configured in Clear Linux (https://github.com/clearlinux/distribution/issues/1004).
        cat >/etc/systemd/system/kubelet.service.d/10-cri.conf <<EOF
[Unit]
After=$cri_daemon.service
EOF
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
        mkdir -p /opt/cni/bin
        for i in /usr/libexec/cni/*;do
            ln -s $i /opt/cni/bin/
        done

        # Reconfiguration done, start daemons. Starting kubelet must wait until kubeadm has created
        # the necessary config files.
        systemctl daemon-reload
        systemctl restart $cri_daemon $containerd_daemon || (
            systemctl status $cri_daemon $containerd_daemon || true
            journalctl -xe || true
            false
        )
        systemctl enable $cri_daemon $containerd_daemon kubelet
    fi
}

install_bundles
