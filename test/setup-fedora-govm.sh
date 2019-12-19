#!/bin/bash
#
# Implements the first-boot configuration of the different virtual machines
# for Fedora running in GoVM.
#
# This script runs *inside* the cluster. All setting env variables
# used by it must be passed in explicitly via ssh and it must run as root.

set -x
set -o errexit # TODO: replace with explicit error checking and error messages.
set -o pipefail

: ${INIT_KUBERNETES:=true}
HOSTNAME=${HOSTNAME:-$1}
IPADDR=${IPADDR:-127.0.0.1}

function error_handler(){
    local line="${1}"
    echo >&2 "ERROR: the command '${BASH_COMMAND}' at $0:${line} failed"
}
trap 'error_handler ${LINENO}' ERR


# For PMEM.
packages+=" ndctl"

# Some additional utilities.
packages+=" device-mapper-persistent-data lvm2"

if ${INIT_KUBERNETES}; then
    # Always use Docker, and always use the same version for reproducibility.
    cat <<'EOF' > /etc/yum.repos.d/docker-ce.repo
[docker-ce-stable]
name=Docker CE Stable - $basearch
baseurl=https://download.docker.com/linux/centos/7/$basearch/stable
enabled=1
gpgcheck=1
gpgkey=https://download.docker.com/linux/centos/gpg
EOF
    packages+=" docker-ce-3:19.03.5-3.el7"

    # Install according to https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/install-kubeadm/
    modprobe br_netfilter
    echo 1 >/proc/sys/net/bridge/bridge-nf-call-iptables
    setenforce 0
    sed -i --follow-symlinks 's/SELINUX=enforcing/SELINUX=disabled/g' /etc/sysconfig/selinux

    cat <<EOF > /etc/yum.repos.d/kubernetes.repo
[kubernetes]
name=Kubernetes
baseurl=https://packages.cloud.google.com/yum/repos/kubernetes-el7-x86_64
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://packages.cloud.google.com/yum/doc/yum-key.gpg https://packages.cloud.google.com/yum/doc/rpm-package-key.gpg
EOF

    # For the sake of reproducibility, use fixed versions.
    # List generated with:
    # for v in 1.13 1.14 1.15 1.16; do for i in kubelet kubeadm kubectl; do echo "$i-$(sudo yum --showduplicates list kubelet | grep " $v"  | sed -e 's/.* \([0-9]*\.[0-9]*\.[0-9]*[^ ]*\).*/\1/' | sort -u  | tail -n 1)"; done; done
    case ${TEST_KUBERNETES_VERSION} in
        1.13) packages+=" kubelet-1.13.12-0 kubeadm-1.13.12-0 kubectl-1.13.12-0";;
        1.14) packages+=" kubelet-1.14.10-0 kubeadm-1.14.10-0 kubectl-1.14.10-0";;
        1.15) packages+=" kubelet-1.15.8-0 kubeadm-1.15.8-0 kubectl-1.15.8-0";;
        1.16) packages+=" kubelet-1.16.5-0 kubeadm-1.16.5-0 kubectl-1.16.5-0";;
        *) echo >&2 "Kubernetes version ${TEST_KUBERNETES_VERSION} not supported, package list in $0 must be updated."; exit 1;;
    esac
    packages+=" --disableexcludes=kubernetes"
fi

# Sometimes we hit a bad mirror and get "Failed to synchronize cache for repo ...".
# https://unix.stackexchange.com/questions/487635/fedora-29-failed-to-synchronize-cache-for-repo-fedora-modular
# suggests to try again after a `dnf update --refresh`, so that's what we do here for
# a maximum of 5 attempts.
cnt=0
while ! yum install -y $packages; do
    if [ $cnt -ge 5 ]; then
        echo "yum install failed repeatedly, giving up"
        exit 1
    fi
    cnt=$(($cnt + 1))
    # If it works, proceed immediately. If it fails, sleep and try again without aborting on an error.
    if ! dnf update --refresh; then
        sleep 20
        dnf update --refresh || true
    fi
done

if $INIT_KUBERNETES; then
    # Upstream kubelet looks in /opt/cni/bin, actual files are in
    # /usr/libexec/cni from
    # containernetworking-plugins-0.8.1-1.fc30.x86_64.
    mkdir -p /opt/cni
    ln -s /usr/libexec/cni /opt/cni/bin

    # Testing may involve a Docker registry running on the build host (see
    # TEST_LOCAL_REGISTRY and TEST_PMEM_REGISTRY). We need to trust that
    # registry, otherwise Docker will fail to pull images from it.
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

    # kubelet must start after the container runtime that it depends on.
    mkdir -p /etc/systemd/system/kubelet.service.d
    cat >/etc/systemd/system/kubelet.service.d/10-cri.conf <<EOF
[Unit]
After=docker.service
EOF

    update-alternatives --set iptables /usr/sbin/iptables-legacy
    systemctl daemon-reload
    systemctl enable --now docker kubelet
fi
