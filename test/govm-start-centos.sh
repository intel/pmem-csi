#!/bin/bash -xeu
# let's have -x enabling tracing while we debug/develop this

# Catch errors in pipelines
set -o pipefail

. $(dirname $0)/govm-start-common.sh
# Select CentOS release and version:
_centosrelease=7
_centosversion=1809

_imgfile=CentOS-${_centosrelease}-x86_64-GenericCloud-${_centosversion}.qcow2
# mirror support for centos not ready yet, dont enable
_mirror=
_wdir=_work/centos-govm

# After boot, at some point sshd becomes reconfigured to allow root login.
# Before that, 1st stage: ssh does not work, 2nd stage: ssh works but refuses root login.
# Then, 3rd stage is reached: root ssh is allowed. We can proceed after that.

# TODO: based on Ubuntu and Centos observation, this function is almost same
# except for user name ubuntu vs centos, so this function can potentially be common,
# once we figure out how to make parametric user name which is inside both single and double quotes
wait_responds_root_ssh () {
    a=$1
    while true; do
        outp=`$_ssh root@$a true`
        retv=$?
        #echo `date` "   ret=$retv outp=[$outp]"
        if [ $retv -eq 0 -a "$outp" != 'Please login as the user "centos" rather than the user "root".' ]; then
	    break
	fi
        sleep 5
    done
}

setup_one_node () {
    a=$1
    echo "============= start setup of host $a ==============="
    set +e
    wait_responds_root_ssh $a
    set -e
    [ -n "$_mirror" ] && set_yum_mirror $a
    [ -n "${http_proxy:-}" ] && set_yum_proxy $a
    $_ssh root@$a "yum update -y"
    [ -n "${http_proxy:-}" ] && set_docker_proxy $a
    set_docker_registry $a
    # install docker after proxy and insecure setting creation, so it can run without restart
    install_docker $a
    init_nvdimm_labels $a
    install_kubeadm $a
    final_tuning $a
}

set_yum_mirror () {
    # TODO: mirror support not added yet
    a=$1
    if [ -f $_wdir/govm_set_yum_mirror.$a.done ]; then
        return 0
    fi
    touch $_wdir/govm_set_yum_mirror.$a.done
}

set_yum_proxy () {
    # proxy for yum
    a=$1
    if [ -f $_wdir/govm_yum_proxy.$a.done ]; then
        return 0
    fi
    $_ssh root@$a "cat >>/etc/yum.conf" <<EOF
proxy=$http_proxy
EOF
    touch $_wdir/govm_yum_proxy.$a.done
}

install_kubeadm () {
    # add Kubernetes key and repo. bionic,cosmic not available so we use latest=xenial
    a=$1
    if [ -f $_wdir/govm_install_kubeadm.$a.done ]; then
	return 0
    fi
    $_ssh root@$a "cat >/etc/yum.repos.d/kubernetes.repo" <<EOF
[kubernetes]
name=Kubernetes
baseurl=https://packages.cloud.google.com/yum/repos/kubernetes-el7-x86_64
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://packages.cloud.google.com/yum/doc/yum-key.gpg
        https://packages.cloud.google.com/yum/doc/rpm-package-key.gpg
EOF
    $_ssh root@$a "modprobe br_netfilter"
    $_ssh root@$a "echo '1' > /proc/sys/net/bridge/bridge-nf-call-iptables"
    $_ssh root@$a "yum install -y kubelet kubeadm kubectl"
    touch $_wdir/govm_install_kubeadm.$a.done
}

install_docker () {
    # note we also install ndctl here, reusing yum install point for convenience
    a=$1
    if [ -f $_wdir/govm_install_docker.$a.done ]; then
	return 0
    fi
    $_ssh root@$a "yum install -y yum-utils device-mapper-persistent-data lvm2"
    $_ssh root@$a "yum-config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo"
    $_ssh root@$a "yum install -y docker-ce ndctl"
    touch $_wdir/govm_install_docker.$a.done
}

final_tuning () {
    # some misc. operations
    a=$1
    if [ -f $_wdir/govm_final_tuning.$a.done ]; then
	return 0
    fi
    $_ssh root@$a "setenforce 0"
    $_ssh root@$a "sed -i --follow-symlinks 's/SELINUX=enforcing/SELINUX=disabled/g' /etc/sysconfig/selinux"
    $_ssh root@$a "systemctl start docker && systemctl enable docker.service"
    $_ssh root@$a "systemctl start kubelet && systemctl enable kubelet.service"
    # silence timesync, fails to connect outside. Other way would be to configure ntp server
    $_ssh root@$a "systemctl stop chronyd.service"
    $_ssh root@$a "systemctl disable chronyd.service"
    
    touch $_wdir/govm_final_tuning.$a.done
}

########################  START   ###################################
# get image, checking is it cached
mkdir -p $_wdir
if [ ! -e $_imgdir/$_imgfile ]; then
    curl https://cloud.centos.org/centos/${_centosrelease}/images/$_imgfile -o $_imgdir/$_imgfile
fi
# TODO/limitation: Currently use num of any goVMs (govm list) for detecting running VMs,
# works while no other goVMs run on same host.
# If we want to become more flexible, we have to implement more intelligent detection of own VMs.

# Current approach creates VMs, but after stopping, it can't restart those.
# TODO: As improvement, we could detect existing but stopped instances and restart.
# Could add a flag like "vm_compose.$a.done" marking compose stage completed.
num_vms=`govm list -f '{{select (filterRegexp . "Name" "host-*") "IP"}}'|wc -l`
if [ $num_vms -ne $NUM_NODES ]; then
    # wipe all flags and host-specific logs, we start from zero state
    rm -f $_wdir/govm_*.done $_wdir/*.out
    # we generate yaml file which is given to "govm compose"
    create_vmhost_manifest ${_centosrelease} govm-centos $_imgfile
    govm compose -f $_wdir/govm-centos-${_centosrelease}.yaml
fi
# Sanity re-check num of VMs again
num_vms=`govm list -f '{{select (filterRegexp . "Name" "host-*") "IP"}}'|wc -l`
if [ $num_vms -ne $NUM_NODES ]; then
    echo "should see $NUM_NODES running but there is $num_vms, exiting"
    exit 1
fi
allnodes_ip=`govm list -f '{{select (filterRegexp . "Name" "host-*") "IP"}}'`
master=`govm list -f '{{select (filterRegexp . "Name" "host-0") "IP"}}'`

# setup nodes in parallel, wait at end for all nodes to be ready
for ip in $allnodes_ip; do
    setup_one_node $ip >$_wdir/$ip.out 2>&1 &
done
wait

# This section needed only if we rebooted after setup, which is not the case now
## wait until all respond to ssh again
#set +e
#for ip in $allnodes_ip; do
#    wait_responds_root_ssh $ip
#done
#set -e

setup_kubeadm_args
# Kubernetes setup on master node:
kubeadm_init
# followed by join by other nodes:
nodes_join
# Copy kube config out from master to host so that kubectl becomes usable on host
copy_kubeconfig_out
# misc. cluster setup and driver deployment
cluster_setup
