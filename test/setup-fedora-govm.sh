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

# add own name to /etc/hosts to avoid external name lookup(s) that produce warning by kubeadm
echo "127.0.0.1 `hostname`" >> /etc/hosts

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
    packages+=" docker-ce-3:19.03.13-3.el7 containerd.io-1.3.7-3.1.el7"

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
    # for v in 1.13 1.14 1.15 1.16 1.17 1.18; do for i in kubelet kubeadm kubectl; do echo "$i-$(sudo dnf --showduplicates list kubelet | grep " $v"  | sed -e 's/.* \([0-9]*\.[0-9]*\.[0-9]*[^ ]*\).*/\1/' | sort -u -n -k 2.6 | tail -n 1)"; done; done
    # Note that -n and -k 2.6 are needed to handle minor versions > 9, and -k 2.6 works only if k8s major versions have form X.XX
    case ${TEST_KUBERNETES_VERSION} in
        1.13) packages+=" kubelet-1.13.12-0 kubeadm-1.13.12-0 kubectl-1.13.12-0";;
        1.14) packages+=" kubelet-1.14.10-0 kubeadm-1.14.10-0 kubectl-1.14.10-0";;
        1.15) packages+=" kubelet-1.15.11-0 kubeadm-1.15.11-0 kubectl-1.15.11-0";;
        1.16) packages+=" kubelet-1.16.9-0 kubeadm-1.16.9-0 kubectl-1.16.9-0";;
        1.17) packages+=" kubelet-1.17.5-0 kubeadm-1.17.5-0 kubectl-1.17.5-0";;
        1.18) packages+=" kubelet-1.18.2-0 kubeadm-1.18.2-0 kubectl-1.18.2-0";;
        1.19) packages+=" kubelet-1.19.1-0 kubeadm-1.19.1-0 kubectl-1.19.1-0";;
        1.20) packages+=" kubelet-1.20.4-0 kubeadm-1.20.4-0 kubectl-1.20.4-0";;
        *) echo >&2 "Kubernetes version ${TEST_KUBERNETES_VERSION} not supported, package list in $0 must be updated."; exit 1;;
    esac
    packages+=" --disableexcludes=kubernetes"
fi

# Sometimes we hit a bad mirror and get "Failed to synchronize cache for repo ...".
# https://unix.stackexchange.com/questions/487635/fedora-29-failed-to-synchronize-cache-for-repo-fedora-modular
# suggests to try again after a `dnf update --refresh`, so that's what we do here for
# a maximum of 5 attempts.
cnt=0
while ! dnf install -y $packages; do
    if [ $cnt -ge 5 ]; then
        echo "dnf install failed repeatedly, giving up"
        exit 1
    fi
    cnt=$(($cnt + 1))
    # If it works, proceed immediately. If it fails, sleep and try again without aborting on an error.
    if ! dnf update -q -y --refresh; then
        sleep 20
        dnf update -q -y --refresh || true
    fi
done

if $INIT_KUBERNETES; then
    # Upstream kubelet looks in /opt/cni/bin, actual files are in
    # /usr/libexec/cni from
    # containernetworking-plugins-0.8.1-1.fc30.x86_64.
    # There is reason why we don't create symlink /opt/cni/bin -> /usr/libexec/cni.
    # Some CNI variants (weave) try to add binaries, which fails if bin/ is symlink
    # We create /opt/cni/bin containing symlinks for every binary:
    mkdir -p /opt/cni/bin
    for i in /usr/libexec/cni/*; do
        if ! [ -e /opt/cni/bin/$(basename $i) ]; then
            ln -s $i /opt/cni/bin/
        fi
    done

    # For containerd, but may also be needed for other CRIs.
    # According to https://kubernetes.io/docs/setup/production-environment/container-runtimes/#prerequisites-1
    cat >/etc/modules-load.d/containerd.conf <<EOF
overlay
br_netfilter
EOF

    modprobe overlay
    modprobe br_netfilter

    cat >/etc/sysctl.d/99-kubernetes-cri.conf <<EOF
net.bridge.bridge-nf-call-iptables  = 1
net.ipv4.ip_forward                 = 1
net.bridge.bridge-nf-call-ip6tables = 1
EOF
    sysctl --system

    # Proxy settings for the different container runtimes are injected into
    # their environment.
    for cri in crio docker containerd; do
        mkdir /etc/systemd/system/$cri.service.d
        cat >/etc/systemd/system/$cri.service.d/proxy.conf <<EOF
[Service]
Environment="HTTP_PROXY=${HTTP_PROXY}" "HTTPS_PROXY=${HTTPS_PROXY}" "NO_PROXY=${NO_PROXY}"
EOF
    done

    # Testing may involve a Docker registry running on the build host (see
    # TEST_LOCAL_REGISTRY and TEST_PMEM_REGISTRY). We need to trust that
    # registry, otherwise the container runtime will fail to pull images from it.

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

    # And for containerd.
    mkdir -p /etc/containerd
    containerd config default >/etc/containerd/config.toml
    for registry in $INSECURE_REGISTRIES; do
        sed -i -e '/\[plugins."io.containerd.grpc.v1.cri".registry.mirrors\]/a \        [plugins."io.containerd.grpc.v1.cri".registry.mirrors."'$registry'"]\n          endpoint = ["http://'$registry'"]' /etc/containerd/config.toml
    done

    # Enable systemd cgroups as described in https://github.com/containerd/containerd/issues/4203#issuecomment-651532765
    sed -i -e 's/runtime_type = "io.containerd.runc.v1"/runtime_type = "io.containerd.runc.v2"/' /etc/containerd/config.toml
    cat >>/etc/containerd/config.toml <<EOF
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
  SystemdCgroup = true
EOF


    containerd_daemon=
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
        containerd)
            cri_daemon=containerd
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
    cat >/etc/systemd/system/kubelet.service.d/10-cri.conf <<EOF
[Unit]
After=$cri_daemon.service
EOF

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
