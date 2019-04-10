#!/bin/bash -e
#
# Implements the first-boot configuration of the different virtual machines.
# See clear-kvm.make for details.

set -o pipefail

NUM_NODES=$1
LAST_NODE=$(($NUM_NODES - 1))

# We run with tracing enabled, but suppress the trace output for some
# parts with 2>/dev/null (in particular, echo commands) to keep the
# output a bit more readable.
set -x

# Source additional configuration and commands.
. $(dirname $0)/test-config.sh

NO_PROXY="$NO_PROXY,${TEST_IP_ADDR}.0/24,10.96.0.0/12,10.244.0.0/16"
PROXY_ENV="env 'HTTP_PROXY=$HTTP_PROXY' 'HTTPS_PROXY=$HTTPS_PROXY' 'NO_PROXY=$NO_PROXY'"

# 127.0.0.x is included here with the hope that the resolver accepts packets
# also via other interfaces. If it doesn't, then TEST_DNS_SERVERS must be set
# explicitly.
dns_servers=${TEST_DNS_SERVERS:-$(grep '^ *nameserver ' /etc/resolv.conf  | sed -e 's/nameserver//' -e "s/127.0.0.[0-9]*/${TEST_IP_ADDR}.1/")}

bundles="cloud-native-basic ${TEST_CLEAR_LINUX_BUNDLES}"

# containers-basic is needed because of the CNI plugins (https://github.com/clearlinux/distribution/issues/256#issuecomment-440998525)
# and for Docker.
bundles="$bundles containers-basic"

kubeadm_args=
kubeadm_args_init=
kubeadm_config_init="apiVersion: kubeadm.k8s.io/v1beta1
kind: InitConfiguration"
kubeadm_config_cluster="apiVersion: kubeadm.k8s.io/v1beta1
kind: ClusterConfiguration"
kubeadm_config_kubelet="apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration"

if [ ! -z ${TEST_FEATURE_GATES} ]; then
    kubeadm_config_kubelet="$kubeadm_config_kubelet
featureGates:
$(IFS=","; for f in ${TEST_FEATURE_GATES};do
echo "  $f" | sed 's/=/: /g'
done)"
    kubeadm_config_cluster="$kubeadm_config_cluster
apiServer:
  extraArgs:
    feature-gates: ${TEST_FEATURE_GATES}"
fi

case $TEST_CRI in
    docker)
        cri_daemon=docker
        # [ERROR SystemVerification]: unsupported docker version: 18.06.1
        kubeadm_args="$kubeadm_args --ignore-preflight-errors=SystemVerification"
        ;;
    crio)
        cri_daemon=cri-o
        # Needed for CRI-O (https://clearlinux.org/documentation/clear-linux/tutorials/kubernetes).
        kubeadm_config_init="$kubeadm_config_init
nodeRegistration:
  criSocket: /run/crio/crio.sock"
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

# Kills a process and all its children.
kill_process_tree () {
    name=$1
    pid=$2

    pids=$(ps -o pid --ppid $pid --no-headers)
    if [ "$pids" ]; then
        kill $pids
    fi
}

# Creates a virtual machine and enables SSH for it. Must be started
# in a sub-shell. The virtual machine is kept running until this
# sub-shell is killed.
setup_clear_img () (
    image=$1
    imagenum=$2
    set -e

    # We use copy-on-write for per-host image files. We don't share as much as we could (for example,
    # each host adds its own bundles), but at least the base image is the same.
    qemu-img create -f qcow2 -o backing_file=clear-kvm-original.img $image

    # Same start script for all virtual machines. It gets called with
    # the image name.
    ln -sf start-clear-kvm _work/start-clear-kvm.$imagenum

    # Determine which machine we build and the parameters for it.
    seriallog=_work/serial.$imagenum.log
    hostname=host-$imagenum
    ipaddr=${TEST_IP_ADDR}.$(($imagenum * 2 + 2))

    # coproc runs the shell commands in a separate process, with
    # stdin/stdout available to the caller.
    coproc sh -c "_work/start-clear-kvm $image | tee $seriallog"

    kill_qemu () {
        (
            touch _work/clear-kvm.$imagenum.terminated
            if [ "$COPROC_PID" ]; then
                kill_process_tree QEMU $COPROC_PID
            fi
        )
    }
    trap kill_qemu EXIT SIGINT SIGTERM

    # bash will detect when the coprocess dies and then unset the COPROC variables.
    # We can use that to check that QEMU is still healthy and avoid "ambiguous redirect"
    # errors when reading or writing to empty variables.
    qemu_running () {
        ( if ! [ "$COPROC_PID" ]; then echo "ERRROR: QEMU died unexpectedly, see error messages above."; exit 1; fi ) 2>/dev/null
    }

    # Wait for certain output from the co-process.
    waitfor () {
        ( term="$1"
          while IFS= read -d : -r x && ! [[ "$x" =~ "$term" ]]; do
              :
          done ) 2>/dev/null
    }

    qemu_running
    ( echo "Waiting for initial root login, see $(pwd)/$seriallog" ) 2>/dev/null
    waitfor "login" <&${COPROC[0]}

    # We get some extra messages on the console that should be printed
    # before we start interacting with the console prompt.
    ( echo "Give Clear Linux some time to finish booting." ) 2>/dev/null
    sleep 5

    qemu_running
    ( echo "Changing root password..." ) 2>/dev/null
    echo "root" >&${COPROC[1]}
    waitfor "New password" <&${COPROC[0]}
    qemu_running
    echo "$(cat _work/passwd)" >&${COPROC[1]}
    waitfor "Retype new password" <&${COPROC[0]}
    qemu_running
    echo "$(cat _work/passwd)" >&${COPROC[1]}

    # SSH needs to be enabled explicitly on Clear Linux.
    echo "mkdir -p /etc/ssh && echo 'PermitRootLogin yes' >> /etc/ssh/sshd_config && mkdir -p .ssh && echo '$(cat _work/id.pub)' >>.ssh/authorized_keys" >&${COPROC[1]}

    # SSH invocations must use that secret key, shouldn't worry about known hosts (because those are going
    # to change), and log into the right machine.
    echo "#!/bin/sh" >_work/ssh-clear-kvm.$imagenum
    echo "exec ssh -oIdentitiesOnly=yes -oStrictHostKeyChecking=no -oUserKnownHostsFile=/dev/null -oLogLevel=error -i $(pwd)/_work/id root@$ipaddr \"\$@\"" >>_work/ssh-clear-kvm.$imagenum
    chmod u+x _work/ssh-clear-kvm.$imagenum

    # Set up the static network configuration. The MAC address must match the one
    # in start_qemu.sh. DNS configuration is taken from /etc/resolv.conf.
    echo "mkdir -p /etc/systemd/network" >&${COPROC[1]}
    for i in "[Match]" "MACAddress=DE:AD:BE:EF:01:0$i" "[Network]" "Address=$ipaddr/24" "Gateway=${TEST_IP_ADDR}.1"; do echo "echo '$i' >>/etc/systemd/network/20-wired.network" >&${COPROC[1]}; done
    for addr in $dns_servers; do echo "echo 'DNS=$addr' >>/etc/systemd/network/20-wired.network" >&${COPROC[1]}; done
    echo "systemctl restart systemd-networkd" >&${COPROC[1]}

    # Disable auto-update. An update of Kubernetes would require
    # manual intervention, and we don't want the updating to interfere
    # with testing.
    _work/ssh-clear-kvm.$imagenum swupd autoupdate --disable

    # Install Kubernetes and additional bundles.
    ( echo "Configuring Kubernetes..." ) 2>/dev/null
    _work/ssh-clear-kvm.$imagenum "$PROXY_ENV swupd bundle-add $bundles"
    _work/ssh-clear-kvm.$imagenum swupd clean

    # Enable IP Forwarding.
    _work/ssh-clear-kvm.$imagenum 'mkdir /etc/sysctl.d && echo net.ipv4.ip_forward = 1 >/etc/sysctl.d/60-k8s.conf && systemctl restart systemd-sysctl'

    # Due to stateless /etc is empty but /etc/hosts is needed by k8s pods.
    # It also expects that the local host name can be resolved. Let's use a nicer one
    # instead of the normal default (clear-<long hex string>).
    _work/ssh-clear-kvm.$imagenum "hostnamectl set-hostname $hostname"
    _work/ssh-clear-kvm.$imagenum "echo 127.0.0.1 localhost >>/etc/hosts"
    _work/ssh-clear-kvm.$imagenum "echo $ipaddr $hostname >>/etc/hosts"

    # br_netfilter must be loaded explicitly on the Clear Linux KVM kernel (and only there),
    # otherwise the required /proc/sys/net/bridge/bridge-nf-call-iptables isn't there.
    _work/ssh-clear-kvm.$imagenum modprobe br_netfilter && _work/ssh-clear-kvm.$imagenum 'echo br_netfilter >>/etc/modules'

    # Disable swap (permanently).
    _work/ssh-clear-kvm.$imagenum systemctl mask $(_work/ssh-clear-kvm.$imagenum cat /proc/swaps | sed -n -e 's;^/dev/\([0-9a-z]*\).*;dev-\1.swap;p')
    _work/ssh-clear-kvm.$imagenum swapoff -a

    # We put config changes in place for both runtimes, even though only one of them will
    # be used by Kubernetes, just in case that someone wants to use them manually.

    # Proxy settings for CRI-O.
    _work/ssh-clear-kvm.$imagenum mkdir /etc/systemd/system/crio.service.d
    _work/ssh-clear-kvm.$imagenum "cat >/etc/systemd/system/crio.service.d/proxy.conf" <<EOF
[Service]
Environment="HTTP_PROXY=${HTTP_PROXY}" "HTTPS_PROXY=${HTTPS_PROXY}" "NO_PROXY=${NO_PROXY}"
EOF

    # Testing may involve a Docker registry running on the build host (see
    # REGISTRY_NAME). We need to trust that registry, otherwise CRI-O
    # will fail to pull images from it.
    _work/ssh-clear-kvm.$imagenum "mkdir -p /etc/containers && cat >/etc/containers/registries.conf" <<EOF
[registries.insecure]
registries = [ '${TEST_IP_ADDR}.1:5000' ]
EOF

    # The same for Docker.
    _work/ssh-clear-kvm.$imagenum mkdir -p /etc/docker
    _work/ssh-clear-kvm.$imagenum "cat >/etc/docker/daemon.json" <<EOF
{ "insecure-registries": ["${TEST_IP_ADDR}.1:5000"] }
EOF

    # Proxy settings for Docker.
    _work/ssh-clear-kvm.$imagenum mkdir -p /etc/systemd/system/docker.service.d/
    _work/ssh-clear-kvm.$imagenum "cat >/etc/systemd/system/docker.service.d/proxy.conf" <<EOF
[Service]
Environment="HTTP_PROXY=$HTTP_PROXY" "HTTPS_PROXY=$HTTPS_PROXY" "NO_PROXY=$NO_PROXY"
EOF

    # Disable the use of Kata containers as default runtime in Docker.
    # The Kubernetes control plan (apiserver, etc.) fails to run otherwise
    # ("Host networking requested, not supported by runtime").
    _work/ssh-clear-kvm.$imagenum "cat >/etc/systemd/system/docker.service.d/51-runtime.conf" <<EOF
[Service]
Environment="DOCKER_DEFAULT_RUNTIME=--default-runtime runc"
EOF

    case $TEST_CRI in
        docker)
             # Choose Docker by disabling the use of CRI-O in KUBELET_EXTRA_ARGS.
            _work/ssh-clear-kvm.$imagenum 'mkdir -p /etc/systemd/system/kubelet.service.d/'
            _work/ssh-clear-kvm.$imagenum "cat >/etc/systemd/system/kubelet.service.d/10-kubeadm.conf" <<EOF
[Service]
Environment="KUBELET_EXTRA_ARGS="
EOF
            ;;
        crio)
            # Nothing to do, it is the default in Clear Linux.
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
    _work/ssh-clear-kvm.$imagenum mkdir -p /opt/cni/bin
    _work/ssh-clear-kvm.$imagenum 'for i in /usr/libexec/cni/*; do ln -s $i /opt/cni/bin/; done'

    # Reconfiguration done, start daemons. Starting kubelet must wait until kubeadm has created
    # the necessary config files.
    _work/ssh-clear-kvm.$imagenum "systemctl daemon-reload && systemctl restart $cri_daemon && systemctl enable $cri_daemon kubelet"

    # Run additional code specified by config.
    ${TEST_CLEAR_LINUX_POST_INSTALL:-true} $imagenum

    set +x
    echo "up and running"
    touch _work/clear-kvm.$imagenum.running
    qemu_running
    wait $COPROC_PID
)

# Create an alias for logging into the master node.
ln -sf ssh-clear-kvm.0 _work/ssh-clear-kvm

# Create all virtual machines in parallel.
declare -a machines
kill_machines () {
    ps xwww --forest
    for i in $(seq 0 $LAST_NODE); do
        if [ "${machines[$i]}" ]; then
            kill_process_tree "QEMU setup #$i" ${machines[$i]}
            wait ${machines[$i]} || true
            machines[$i]=
        fi
    done
    rm -f _work/clear-kvm.*.running _work/clear-kvm.*.terminated
}
trap kill_machines EXIT SIGINT SIGTERM
rm -f _work/clear-kvm.*.running _work/clear-kvm.*.terminated _work/clear-kvm.[0-9].img*
for i in $(seq 0 $LAST_NODE); do
    setup_clear_img _work/clear-kvm.$i.img $i &> >(sed -e "s/^/$i:/") &
    machines[$i]=$!
done

# setup_clear_img notifies us when VMs are ready by creating a
# clear-kvm.*.running file.
all_running () {
    [ $(ls -1 _work/clear-kvm.*.running 2>/dev/null | wc -l) -eq $((LAST_NODE + 1)) ]
}

# Same for termination.
some_failed () {
    [ $(ls -1 _work/clear-kvm.*.terminated 2>/dev/null | wc -l) -gt 0 ]
}

# Wait until all are up and running.
while ! all_running; do
    sleep 1
    if some_failed; then
        echo
        echo "The following virtual machines failed unexpectedly, see errors above:"
        ls -1 _work/clear-kvm.*.terminated | sed -e 's;_work/clear-kvm.\(.*\).terminated;    #\1;'
        echo
        exit 1
    fi
done 2>/dev/null

_work/ssh-clear-kvm.0 "cat >/tmp/kubeadm-config.yaml" <<EOF
$kubeadm_config_init
---
$kubeadm_config_kubelet
---
$kubeadm_config_cluster
EOF

# TODO: it is possible to set up each node in parallel, see
# https://kubernetes.io/docs/reference/setup-tools/kubeadm/kubeadm-init/#automating-kubeadm

kubeadm_args_init="$kubeadm_args_init --config=/tmp/kubeadm-config.yaml"
_work/ssh-clear-kvm.0 $PROXY_ENV kubeadm init $kubeadm_args $kubeadm_args_init | tee _work/clear-kvm-kubeadm.0.log
_work/ssh-clear-kvm.0 mkdir -p .kube
_work/ssh-clear-kvm.0 cp -i /etc/kubernetes/admin.conf .kube/config

# Done.
( echo "Use $(pwd)/clear-kvm-kube.config as KUBECONFIG to access the running cluster." ) 2>/dev/null
_work/ssh-clear-kvm.0 'cat /etc/kubernetes/admin.conf' | sed -e "s;https://.*:6443;https://${TEST_IP_ADDR}.2:6443;" >_work/clear-kvm-kube.config

# Verify that Kubernetes works by starting it and then listing pods.
# We also wait for the node to become ready, which can take a while because
# images might still need to be pulled. This can take minutes, therefore we sleep
# for one minute between output.
( echo "Waiting for Kubernetes cluster to become ready..." ) 2>/dev/null
_work/kube-clear-kvm
while ! _work/ssh-clear-kvm.0 kubectl get nodes | grep -q 'Ready'; do
    _work/ssh-clear-kvm.0 kubectl get nodes
    _work/ssh-clear-kvm.0 kubectl get pods --all-namespaces
    sleep 60
done
_work/ssh-clear-kvm.0 kubectl get nodes
_work/ssh-clear-kvm.0 kubectl get pods --all-namespaces

# Doing the same locally only works if we have kubectl.
if command -v kubectl >/dev/null; then
    kubectl --kubeconfig _work/clear-kvm-kube.config get pods --all-namespaces
fi

# Run additional commands specified in config.
${TEST_CONFIGURE_POST_MASTER}

# Let the other machines join the cluster.
# Join multi-lines using 'sed' so that grep finds whole line
for i in $(seq 1 $LAST_NODE); do
    _work/ssh-clear-kvm.$i $(sed -z 's/\\\n//g' _work/clear-kvm-kubeadm.0.log |grep "kubeadm join.*token" ) $kubeadm_args
done

# From https://kubernetes.io/docs/setup/independent/create-cluster-kubeadm/#pod-network
_work/ssh-clear-kvm $PROXY_ENV kubectl apply -f https://raw.githubusercontent.com/coreos/flannel/bc79dd1505b0c8681ece4de4c0d86c5cd2643275/Documentation/kube-flannel.yml

# Install addon storage CRDs, needed if certain feature gates are enabled.
# Only applicable to Kubernetes 1.13 and older. 1.14 will have them as builtin APIs.
# Temporarily install also on 1.14 until we migrate to sidecars which use the builtin beta API.
if _work/ssh-clear-kvm $PROXY_ENV kubectl version | grep -q '^Server Version.*Major:"1", Minor:"1[01234]"'; then
    if [[ "$TEST_FEATURE_GATES" == *"CSINodeInfo=true"* ]]; then
        _work/ssh-clear-kvm $PROXY_ENV kubectl create -f https://raw.githubusercontent.com/kubernetes/kubernetes/release-1.13/cluster/addons/storage-crds/csinodeinfo.yaml
    fi
    if [[ "$TEST_FEATURE_GATES" == *"CSIDriverRegistry=true"* ]]; then
        _work/ssh-clear-kvm $PROXY_ENV kubectl create -f https://raw.githubusercontent.com/kubernetes/kubernetes/release-1.13/cluster/addons/storage-crds/csidriver.yaml
    fi
fi

# Run additional commands specified in config.
${TEST_CONFIGURE_POST_ALL}

# Clean shutdown.
for i in $(seq 0 $LAST_NODE); do
    _work/ssh-clear-kvm.$i shutdown now || true
done
for i in $(seq 0 $LAST_NODE); do
    wait ${machines[$i]}
    machines[$i]=
done

echo "done"
