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

HOSTNAME=${HOSTNAME:-$1}
IPADDR=${IPADDR:-127.0.0.1}

function error_handler(){
    local line="${1}"
    echo >&2 "ERROR: the command '${BASH_COMMAND}' at $0:${line} failed"
}
trap 'error_handler ${LINENO}' ERR

# Always use Docker.
cat <<'EOF' > /etc/yum.repos.d/docker-ce.repo
[docker-ce-stable]
name=Docker CE Stable - $basearch
baseurl=https://download.docker.com/linux/centos/7/$basearch/stable
enabled=1
gpgcheck=1
gpgkey=https://download.docker.com/linux/centos/gpg
EOF
packages+=" docker-ce"

# For PMEM.
packages+=" ndctl"

# Some additional utilities.
packages+=" device-mapper-persistent-data lvm2"

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
packages+=" kubelet kubeadm kubectl --disableexcludes=kubernetes"

yum install -y $packages

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
