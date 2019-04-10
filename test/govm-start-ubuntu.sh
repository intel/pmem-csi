#!/bin/bash -xeu
# let's have -x enabling tracing while we debug/develop this

# Catch errors in pipelines
set -o pipefail

. $(dirname $0)/govm-start-common.sh
# Select Ubuntu release: bionic=18.04 cosmic=18.10 disco=19.04
#_ubunturelease=bionic
#_ubunturelease=cosmic
_ubunturelease=disco
# TODO: should Ubuntu release be argument? If argument, we get rid of
# switching between two lines here, but we add selecting need elsewhere.
# If we call this from other script or Makefile, we need to code release there.
# Here we can fall to default "disco" if value not given.

# Kernel is updated separately. Here are some tried, known-to-work versions
# Recent 4.19:
#kver=4.19.34
#kern1=${kver}-041934
#kern2=201904051741

# Recent 4.20:
#kver=4.20.17
#kern1=${kver}-042017
#kern2=201903190933

# Recent 5.0:
kver=5.0.7
kern1=${kver}-050007
kern2=201904052141

kurl=https://kernel.ubuntu.com/~kernel-ppa/mainline/v${kver}

_imgfile=${_ubunturelease}-server-cloudimg-amd64.img
_mirror=
_wdir=_work/ubuntu-govm


lk1=linux-headers-${kern1}_${kern1}.${kern2}_all.deb
lk2=linux-headers-${kern1}-generic_${kern1}.${kern2}_amd64.deb
lk3=linux-image-unsigned-${kern1}-generic_${kern1}.${kern2}_amd64.deb
lk4=linux-modules-${kern1}-generic_${kern1}.${kern2}_amd64.deb

fetch_kernel_files () {
    if [ ! -f $_imgdir/$lk1 ]; then
        curl $kurl/$lk1 -o $_imgdir/$lk1
    fi
    if [ ! -f $_imgdir/$lk2 ]; then
        curl $kurl/$lk2 -o $_imgdir/$lk2
    fi
    if [ ! -f $_imgdir/$lk3 ]; then
        curl $kurl/$lk3 -o $_imgdir/$lk3
    fi
    if [ ! -f $_imgdir/$lk4 ]; then
        curl $kurl/$lk4 -o $_imgdir/$lk4
    fi
}

update_kernel () {
    a=$1
    if [ -f $_wdir/govm_update_kernel.$a.done ]; then
        return 0
    fi
    # install different kernel to get functional pmem devices, and reboot
    $_ssh root@$a "rm -f /tmp/linux-*.deb"
    $_scp $_imgdir/$lk1 $_imgdir/$lk2 $_imgdir/$lk3 $_imgdir/$lk4 root@$a:/tmp/
    $_ssh root@$a "dpkg -i /tmp/linux-*.deb"
    $_ssh root@$a "shutdown -r now"

    touch $_wdir/govm_update_kernel.$a.done
}

# After boot, at some point sshd becomes reconfigured to allow root login.
# Before that, 1st stage: ssh does not work, 2nd stage: ssh works but refuses root login.
# Then, 3rd stage is reached: root ssh is allowed. We can proceed after that.
wait_responds_root_ssh () {
    a=$1
    while true; do
        outp=`$_ssh root@$a true`
        retv=$?
        #echo `date` "   ret=$retv outp=[$outp]"
        if [ $retv -eq 0 -a "$outp" != 'Please login as the user "ubuntu" rather than the user "root".' ]; then
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
    [ -n "$_mirror" ] && set_apt_mirror $a
    [ -n "${http_proxy:-}" ] && set_apt_proxy $a
    $_ssh root@$a "apt update -y"
    [ -n "${http_proxy:-}" ] && set_docker_proxy $a
    set_docker_registry $a
    # install docker after proxy and insecure setting creation, so it can run without restart
    install_docker $a
    init_nvdimm_labels $a
    install_kubeadm $a
    final_tuning $a
    update_kernel $a
}

set_apt_mirror () {
    # override all repos. This is called only if mirror value exists
    a=$1
    if [ -f $_wdir/govm_set_apt_mirror.$a.done ]; then
        return 0
    fi
    $_ssh root@$a "cat > /etc/apt/sources.list" <<EOF
deb $_mirror ${_ubunturelease} main restricted
deb $_mirror ${_ubunturelease}-updates main restricted
deb $_mirror ${_ubunturelease} universe
deb $_mirror ${_ubunturelease}-updates universe
deb $_mirror ${_ubunturelease} multiverse
deb $_mirror ${_ubunturelease}-updates multiverse
deb $_mirror ${_ubunturelease}-backports main restricted universe multiverse
deb $_mirror ${_ubunturelease}-security main restricted
deb $_mirror ${_ubunturelease}-security universe
deb $_mirror ${_ubunturelease}-security multiverse
EOF
    touch $_wdir/govm_set_apt_mirror.$a.done
}

set_apt_proxy () {
    # proxy for apt
    a=$1
    if [ -f $_wdir/govm_apt_proxy.$a.done ]; then
        return 0
    fi
    $_ssh root@$a "cat >/etc/apt/apt.conf.d/00proxy" <<EOF
Acquire::http::Proxy "$http_proxy";
EOF
    # ... with exception for mirror
    if [ -n "$_mirror" ]; then
        $_ssh root@$a "cat >>/etc/apt/apt.conf.d/00proxy" <<EOF
Acquire::http::Proxy::linux-ftp.fi.intel.com  "DIRECT";
EOF
    fi
    touch $_wdir/govm_apt_proxy.$a.done
}

install_kubeadm () {
    # add Kubernetes key and repo. bionic,cosmic not available so we use latest=xenial
    a=$1
    if [ -f $_wdir/govm_install_kubeadm.$a.done ]; then
	return 0
    fi
    $_ssh root@$a "https_proxy=${http_proxy:-} curl -s https://packages.cloud.google.com/apt/doc/apt-key.gpg | apt-key add"
    $_ssh root@$a "apt-add-repository \"deb http://apt.kubernetes.io/ kubernetes-xenial main\""
    $_ssh root@$a "apt install kubeadm -y"
    touch $_wdir/govm_install_kubeadm.$a.done
}

install_docker () {
    # note we also install ndctl here, reusing apt install point for convenience
    a=$1
    if [ -f $_wdir/govm_install_docker.$a.done ]; then
	return 0
    fi
    $_ssh root@$a "apt install ndctl docker.io -y"
    touch $_wdir/govm_install_docker.$a.done
}

final_tuning () {
    # some misc. operations
    a=$1
    if [ -f $_wdir/govm_final_tuning.$a.done ]; then
	return 0
    fi
    $_ssh root@$a "systemctl enable docker.service"
    # silence timesync, fails to connect outside. Other way would be to configure ntp server
    $_ssh root@$a "systemctl stop systemd-timesyncd"
    $_ssh root@$a "systemctl disable systemd-timesyncd"
    touch $_wdir/govm_final_tuning.$a.done
}

########################  START   ###################################
# get image, checking is it cached
mkdir -p $_wdir
if [ ! -e $_imgdir/$_imgfile ]; then
    curl https://cloud-images.ubuntu.com/${_ubunturelease}/current/$_imgfile -o $_imgdir/$_imgfile
fi
fetch_kernel_files
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
    # we generate yaml file which is given to "govm compose",
    # to avoid repetition at 2 levels: 1) Ubuntu releases 2) host blocks in yaml file
    create_vmhost_manifest ${_ubunturelease} govm-ubuntu ${_imgfile}
    govm compose -f $_wdir/govm-ubuntu-${_ubunturelease}.yaml
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
# here nodes got "reboot" cmd

# wait until all respond to ssh again
set +e
for ip in $allnodes_ip; do
    wait_responds_root_ssh $ip
done
set -e

setup_kubeadm_args
# Kubernetes setup on master node:
kubeadm_init
# followed by join by other nodes:
nodes_join
# Copy kube config out from master to host so that kubectl becomes usable on host
copy_kubeconfig_out
# misc. cluster setup and driver deployment
cluster_setup
