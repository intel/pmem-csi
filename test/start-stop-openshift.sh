#!/bin/bash

# These make rules start an OpenShift cluster using virt-manager.
#
# Prerequisites:
# - openshift-baremetal-install binary
#
# Install instructions from Jonathan Halliday/RedHat
#
# First you'll need a quite recent installer. Old ones, including all 
# current stable releases, have bugs that would either prevent things from 
# working at all or require additional workarounds. The most fun one was 
# that, despite using masters=1 as a config option, they try to form a 
# quorum=3 etcd cluster and thus can't work with less than 3 master nodes, 
# which is something of a pain unless you have a very generously sized 
# libvirtd host at your disposal.
#
# In addition to needing a recent installer, you need one built with 
# libvirtd support, which is not standard. It's a dev only feature that's 
# disabled in most production builds. Fortunately there is a loophole that 
# saves you having to custom-compile an installer from source (though that 
# works too...)  The baremetal mode installer, which is supported, makes 
# use of some of the libvirtd code, so unlike the main installers it's 
# actually compiled with livirtd features enabled, though you can't get 
# SLA support for using them. So to do a libvirtd install, you need a 
# baremetal installer.  Confused yet? yeah, me too.
#
# Anyhow, the baremetal mode installer is not directly available for 
# download, so you need to grab the client and then use that to fetch the 
# installer. Intuitive design, right? :-)
#
# https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp-dev-preview/4.8.0-0.nightly-2021-05-26-071911/openshift-client-linux.tar.gz
#
# That one definitely works. All the nightlies after that date 
# theoretically should too, but I don't yet have a CI that explicitly 
# checks them, so no promises.
#
# Once you have the client, you need a pull secret, which is basically an 
# auth token for access to the commercial-but-zero-cost binary parts of 
# OpenShift. Again you can build them from source instead, but that's more 
# pain than most people enjoy. So, go to
#
# https://cloud.redhat.com/openshift/install/metal
#
# dev accounts are free if you don't already have one. Use the account to 
# sign in, then choose installer provisioned infrastructure. Get the pull 
# secret, ignore the rest as you're using nightly builds instead of the 
# bits from there.
#
# Now you need to tell the client to use the pull secret to fetch the 
# installer binary. For that, you need to specify which version of the 
# installer you want to get, as it's not necessarily exactly the same as 
# the version of the client, though when you're later using the client to 
# talk to the installed cluster, they do to have to be more or less the 
# same version or you have compatibility problems...
#
# Look at the release.txt for the installer nightly (in this case the same 
# nightly as the client) and grab the 'pull from' line:
#
# http://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp-dev-preview/4.8.0-0.nightly-2021-05-26-071911/release.txt
#
# "Pull From: 
# quay.io/openshift-release-dev/ocp-release-nightly@sha256:ab62a4104ff3d2a287f4167962241352c392a007d675ecdbc6f195da01d18f08"
#
# then feed that url to the client:
#
# ./oc adm release -a /path/to/pull_secret.json extract 
# quay.io/openshift-release-dev/ocp-release-nightly@sha256:ab62a4104ff3d2a287f4167962241352c392a007d675ecdbc6f195da01d18f08 
# --command=openshift-baremetal-install -to /path/openshift-baremetal-install
#
# Patrick:
# The libvirt libs on Debian Buster are too old for that binary.
# Can be solved by downloading the libvirt 7.0.0 source deb
# from Debian Testing, building it and installing the resulting
# deb files.
#
# - one-time libvirt setup (from Jonathan again):
#
# Start by configuring the host. libvirtd installs work by talking to the 
# deamon over a socket, which needs turning on as it's not default. Since 
# it's basically running as root, it also needs to be open to the cluster 
# but not the whole world. Instructions at
#
# https://github.com/openshift/installer/tree/master/docs/dev/libvirt#one-time-setup
#
# when you get to the part where you're setting up the dnsmasq config so 
# that the host can find the cluster, the line (assuming clusterName=tt 
# and baseDomain=testing, remember the values you choose for use in the 
# installer in few moments)
#
# server=/tt.testing/192.168.126.1
#
# is necessary, but not sufficient. You also need to wildcard the apps 
# subdomain:
#
# address=/.apps.tt.testing/192.168.126.51
#
# The .51 addr is the first (only) worker node the cluster will bring up, 
# so that DNS entry will act as the gateway/loadbalancer, sending ingress 
# traffic to the cluster.

TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname $(readlink -f $0))}
source ${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}
CLUSTER=${CLUSTER:-pmem-openshift}
REPO_DIRECTORY=${REPO_DIRECTORY:-$(dirname $TEST_DIRECTORY)}
RESOURCES_DIRECTORY=${RESOURCES_DIRECTORY:-${REPO_DIRECTORY}/_work/resources}
CLUSTER_DIRECTORY="${CLUSTER_DIRECTORY:-${REPO_DIRECTORY}/_work/${CLUSTER}}"
SSH_KEY="${SSH_KEY:-${RESOURCES_DIRECTORY}/id_rsa}"
SSH_PUBLIC_KEY="${SSH_KEY}.pub"

SSH_TIMEOUT=120
SSH_ARGS="-oIdentitiesOnly=yes -oStrictHostKeyChecking=no \
        -oUserKnownHostsFile=/dev/null -oLogLevel=error \
        -i ${SSH_KEY}"
SSH_ARGS+=" -oServerAliveInterval=15" # required for disruptive testing, to detect quickly when the machine died

# Use the system context. Same as "sudo virsh".
VIRSH="virsh -c qemu+tcp://localhost/system"

die () {
    echo >&2 "ERROR: $@"
    exit 1
}

run () {
    echo >&2 "INVOKING: $@"
    "$@"
}

# Destroys *all* virt-manager resources for the TEST_VIRT_CLUSTER_NAME.
cleanup () {
    echo "Resources:"
    $VIRSH list --all || die "list domains"
    $VIRSH pool-list --all || die "list pools"
    $VIRSH net-list --all || die "list nets"

    for domain in $($VIRSH list --all --name | grep ^$TEST_VIRT_CLUSTER_NAME-); do
        if [ "$($VIRSH domstate $domain)" = "running" ]; then
            run $VIRSH destroy $domain || die "destroy machine $domain"
        fi
        run $VIRSH undefine $domain || die "undefine machine $domain"
    done
    $VIRSH net-list --all | grep "^ $TEST_VIRT_CLUSTER_NAME-" | while read -r net state other; do
        if [ "$state" = "active" ]; then
            run $VIRSH net-destroy $net || die "destroy net $net"
        fi
        run $VIRSH net-undefine $net || die "undefine net $net"
    done
    $VIRSH pool-list --all | grep "^ $TEST_VIRT_CLUSTER_NAME-" | while read -r pool state other; do
        for vol in $($VIRSH vol-list $pool | grep "^ $TEST_VIRT_CLUSTER_NAME-" | sed -e 's/ \([^ ]*\).*/\1/'); do
            run $VIRSH vol-delete --pool $pool $vol || die "delete volume $vol"
        done
        if [ "$state" == "active" ]; then
            run $VIRSH pool-destroy $pool || die "destroy pool $pool"
        fi
        run $VIRSH pool-delete $pool || die "delete pool $pool"
        run $VIRSH pool-undefine $pool || die "undefine pool $pool"
    done

    rm -rf "$CLUSTER_DIRECTORY"
}

check () {
    if [ ! "$TEST_OPENSHIFT_PULL_SECRET" ]; then
        die "TEST_OPENSHIFT_PULL_SECRET must be set to the {\"auths\"... string from https://cloud.redhat.com/openshift/install/metal"
    fi
    $VIRSH list --all 2>/dev/null >/dev/null || die "virt-manager does not work, this command failed: $VIRSH list --all"
    $TEST_OPENSHIFT_INSTALLER >/dev/null || die "TEST_OPENSHIFT_INSTALLER=$TEST_OPENSHIFT_INSTALLER failed"
}

create_cluster () (
    mkdir -p "$CLUSTER_DIRECTORY"
    cd "$CLUSTER_DIRECTORY"

    cat >stop.sh <<EOF
#!/bin/sh
"$TEST_DIRECTORY/start-stop-openshift.sh" stop
EOF
    chmod u+x stop.sh

    virbr=$(set -o pipefail; ip addr show virbr0 | grep '^    inet' | sed -e 's;.*inet \([^/]*\)/.*;\1;') || die "determine virb0 network IP"

    cat >install-config.yaml <<EOF
apiVersion: v1
baseDomain: ${TEST_VIRT_CLUSTER_DOMAIN}
compute:
- architecture: amd64
  hyperthreading: Enabled
  name: worker
  platform: {}
  # Can be increased later with
  # oc scale --replicas=2 machineset tt-foo-worker -n openshift-machine-api
  replicas: 1
controlPlane:
  architecture: amd64
  hyperthreading: Enabled
  name: master
  platform: {}
  replicas: 1
metadata:
  creationTimestamp: null
  name: ${TEST_VIRT_CLUSTER_NAME}
networking:
  clusterNetwork:
  - cidr: 10.128.0.0/14
    hostPrefix: 23
  machineNetwork:
  - cidr: 192.168.126.0/24
  networkType: OpenShiftSDN
  serviceNetwork:
  - 172.30.0.0/16
platform:
  libvirt:
    URI: qemu+tcp://$virbr/system
    network:
      if: tt0
publish: External
pullSecret: '${TEST_OPENSHIFT_PULL_SECRET}'
sshKey: |
  $(cat "$SSH_PUBLIC_KEY")
EOF

    run $TEST_OPENSHIFT_INSTALLER create manifests || die "failed to create manifests"

    cp -r openshift openshift.orig

    # Increase on master. The default of 8GiB is too low.
    sed -i -e "s/domainMemory: $((8 * 1024))\$/domainMemory: $((16 * 1024))/" openshift/*api_master-machines-[0-9].yaml || die "set master memory"

    # With the default of 8GiB, many pods get preempted.
    sed -i -e "s/domainMemory: .*/domainMemory: $TEST_OPENSHIFT_NORMAL_MEM_SIZE/" openshift/*machineset-[0-9].yaml || die "set worker memory"

    diff -r openshift.orig openshift

    # Start cluster creation. While that runs, we need to wait
    # for the network to be configured and then fix it.
    (
        while true; do
            if $VIRSH net-list --all | grep -q "^ $TEST_VIRT_CLUSTER_NAME-.* active"; then
                # This accesses the "oauth-openshift" service through the Ingress service
                # on the first node.
                run $VIRSH net-update $($VIRSH net-list --name | grep "^$TEST_VIRT_CLUSTER_NAME-") \
                    add dns-host "<host ip='192.168.126.51'><hostname>oauth-openshift.apps.$TEST_VIRT_CLUSTER_NAME.$TEST_VIRT_CLUSTER_DOMAIN</hostname></host>"
                exit 0
            fi
            sleep 1
        done
    ) &
    pid=$!
    trap "kill -9 $pid" EXIT
    $TEST_OPENSHIFT_INSTALLER create cluster || die "create cluster"

    # Link it where PMEM-CSI test scripts expect it.
    ln -s auth/kubeconfig kube.config || die "no kubeconfig?!"

    export KUBECONFIG=`pwd`/kube.config

    # "master" sorts before "worker". PMEM-CSI expects host #0 to be the master.
    nodes=$(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}') || die "list nodes"
    nodes=$(echo "$nodes" | sort -u)
    i=0
    for node in $nodes; do
        cat >ssh.$i <<EOF
#!/bin/sh
exec ssh $SSH_ARGS core@$node.$TEST_VIRT_CLUSTER_NAME.$TEST_VIRT_CLUSTER_DOMAIN "\$@"
EOF
        chmod u+x ssh.$i
        ln -s ssh.$i ssh.$node
        i=$((i + 1))
    done

    # Tests expect that "ssh.0 kubectl" works.
    ./ssh.0 mkdir .kube
    ./ssh.0 "cat >.kube/config" <kube.config

    for ssh in ./ssh.[0-9]*; do
        # ndctl is also expected. Core OS doesn't have it, but we can fake it
        # by creating a wrapper script around podman.
        run $ssh "cat >ndctl" <<EOF
#!/bin/sh
sudo podman run -u 0:0 -i --privileged --rm docker.io/intel/pmem-csi-driver:v1.0.1 ndctl "\$@"
EOF
        run $ssh chmod a+rx ndctl
        run $ssh sudo mv ndctl /usr/local/bin/
        # Now we just need to ensure that "sudo ndctl" finds it there.
        run $ssh /bin/sh <<EOF
sudo sed -i -e 's;\(secure_path.*\);\1:/usr/local/bin;' /etc/sudoers
EOF

        # This part would be nice to have, but doesn't currently work
        # because TEST_LOCAL_REGISTRY is not set correctly by default.
        # Better use Docker Hub:
        # cat test/test-config.d/xxx_external-cluster.sh
        # TEST_LOCAL_REGISTRY=docker.io/pohly
        # TEST_PMEM_REGISTRY=docker.io/pohly
        # TEST_BUILD_PMEM_REGISTRY=docker.io/pohly

#         run $ssh sudo mkdir -p /etc/containers
#         run $ssh "sudo tee /etc/containers/registries.conf" <<EOF
# [registries.insecure]
# registries = [ $(echo $INSECURE_REGISTRIES $TEST_LOCAL_REGISTRY | sed 's|^|"|g;s| |", "|g;s|$|"|') ]
# EOF
    done

    cat >restart.sh <<EOF
#!/bin/sh
"$TEST_DIRECTORY/start-stop-openshift.sh" restart
EOF
    chmod u+x restart.sh

    # Determine actual Kubernetes version and record for other tools which need
    # to know without being able to call kubectl.
    ./ssh.0 kubectl version --short | \
        grep 'Server Version' | \
        sed -e 's/.*: v\([0-9]*\)\.\([0-9]*\)\..*/\1.\2/' \
        >kubernetes.version
)

# Edits the configuration of all machines (including master) so that they have PMEM.
# Only the workers are prepared for PMEM-CSI. The PMEM on the master is used for
# the raw conversion test.
add_pmem () (
    cd "$CLUSTER_DIRECTORY"
    total_bytes=$((TEST_OPENSHIFT_PMEM_MEM_SIZE * 1024 * 1024 + $TEST_OPENSHIFT_PMEM_LABEL_SIZE))

    for domain in $($VIRSH list --all --name | grep ^$TEST_VIRT_CLUSTER_NAME-); do
        # The labels are stored inside the same file as the PMEM itself.
        # We have to make it large enough for both and then specify
        # the combined size below.
        nvdimm="$(pwd)$domain.nvdimm"
        run truncate -s $total_bytes $nvdimm || die "create file $nvdimm"

        # We must shut down cleanly to preserve all current changes.
        # A "destroy" is faster, but can corrupt the filesystem.
        $VIRSH shutdown $domain || die "stop domain $domain"
        while $VIRSH list --all | grep -q "$domain.*running\$"; do
            sleep 5
        done

        $VIRSH dumpxml $domain >$domain.xml
        # There is already a memory and currentMemory, but we must
        # also set the upper limit. If the machine is already 8 GB and
        # we want 1 GB for pmem,
        #
        # <maxMemory slots='16' unit='KiB'>9437184</maxMemory>
        #
        # Also nvdimm does not work without numa, which the cpu
        # section does not have by default. So that needs to be added
        # too
        #
        #   <cpu mode='host-passthrough' check='partial'>
        #     <numa>
        #       <cell id='0' cpus='0-3' memory='8388608' unit='KiB'/>
        #     </numa>
        #   </cpu>
        #
        # Now we can add the nvdimm, this goes in the devices section:
        #
        #   <memory model='nvdimm'>
        #     <source>
        #         <path>/var/lib/libvirt/images/nvdimm1</path>
        #         <alignsize unit='KiB'>2048</alignsize>
        #     </source>
        #     <target>
        #       <size unit='KiB'>1048576</size>
        #       <node>0</node>
        #       <label>
        #         <size unit='KiB'>131072</size>
        #       </label>
        #     </target>
        #     <address type='dimm' slot='0'/>
        #   </memory>
        #
        # The <pmem/> must not be used, depending on the QEMU version (?)
        # it triggers an error:
        # Lack of libpmem support while setting the 'pmem=on' of memory-backend-file '(null)'. We can't ensure data persistence.
        run sed \
            -e "/<maxMemory/d" \
            -e "/currentMemory/a <maxMemory slots='16' unit='B'>$((TEST_OPENSHIFT_NORMAL_MEM_SIZE * 1024 * 1024 + $total_bytes))</maxMemory>" \
            -e "s;<cpu .*;<cpu mode='host-passthrough' check='partial'><numa><cell id='0' cpus='0-3' memory='$TEST_OPENSHIFT_NORMAL_MEM_SIZE' unit='MiB'/></numa></cpu>;" \
            -e "/<devices>/a <memory model='nvdimm'><source><path>$nvdimm</path><alignsize unit='KiB'>2048</alignsize></source><target><size unit='B'>$total_bytes</size><node>0</node><label><size unit='B'>$TEST_OPENSHIFT_PMEM_LABEL_SIZE</size></label></target><address type='dimm' slot='0'/></memory>" \
            $domain.xml >$domain-pmem.xml
        run diff -c $domain.xml $domain-pmem.xml

        # There's dumpxml, but no restorexml. We emulate it with a fake editor.
        cat >editor.sh <<EOF
#!/bin/sh

cp $domain-pmem.xml "\$1"
EOF
        chmod u+x editor.sh
        EDITOR=$(pwd)/editor.sh $VIRSH edit $domain || die "edit domain $domain"
        $VIRSH start $domain || die "restart domain $domain"
    done
)

wait_for_vms () {
    start=$SECONDS
    while [ $((SECONDS - $start)) -lt 120 ]; do
        down=
        for domain in $($VIRSH list --all --name | grep ^$TEST_VIRT_CLUSTER_NAME-); do
            if ! ssh $SSH_ARGS core@$domain.$TEST_VIRT_CLUSTER_NAME.$TEST_VIRT_CLUSTER_DOMAIN 2>/dev/null true; then
                down+=" $domain"
            fi
        done
        if [ ! "$down" ]; then
            return 0
        fi
    done

    echo >&2 "ERROR: timed out waiting for all VMs to be running. Currently down:$down"
    return 1
}

restart () {
    for domain in $($VIRSH list --all --name | grep ^$TEST_VIRT_CLUSTER_NAME-); do
        $VIRSH destroy $domain || die "stop domain $domain"
        $VIRSH start $domain || die "restart domain $domain"
    done
    wait_for_vms
}

init_pmem_regions() {
    for domain in $($VIRSH list --all --name | grep ^$TEST_VIRT_CLUSTER_NAME-.*worker); do
        run ssh $SSH_ARGS core@$domain.$TEST_VIRT_CLUSTER_NAME.$TEST_VIRT_CLUSTER_DOMAIN \
            sudo podman run -u 0:0 -i --privileged --rm docker.io/intel/pmem-csi-driver:v1.0.1 /bin/sh <<EOF
set -xe
ndctl disable-region region0
ndctl init-labels nmem0
ndctl enable-region region0
EOF
        run $CLUSTER_DIRECTORY/ssh.0 kubectl label nodes/$domain $TEST_PMEM_NODE_LABEL
    done
}

case "$1" in
    start)
        if [ -d "$CLUSTER_DIRECTORY" ]; then
            echo "$CLUSTER_DIRECTORY already exists, not re-creating it."
            exit 0
        fi
        check && \
        create_cluster && \
            add_pmem && \
            wait_for_vms && \
            init_pmem_regions
        ;;
    stop)
        cleanup
        ;;
    create)
        create_cluster
        ;;
    configure)
        add_pmem
        ;;
    init)
        init_pmem_regions
        ;;
    restart)
        restart
        ;;
    help|--help)
        echo "usage: $0 start|stop"
        ;;
esac
