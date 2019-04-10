. $(dirname $0)/test-config.sh
NUM_NODES=4
# override TEST_IP_ADDR that comes from test-config.sh (as clear-specific value) with govm value
TEST_IP_ADDR=172.17.0
NO_PROXY="${NO_PROXY:-},${TEST_IP_ADDR}.0/24,10.96.0.0/12,10.244.0.0/16"
PROXY_ENV="env 'HTTP_PROXY=${http_proxy:-}' 'HTTPS_PROXY=${http_proxy:-}' 'NO_PROXY=$NO_PROXY'"
_sshopts='-oIdentitiesOnly=yes -oStrictHostKeyChecking=no -oUserKnownHostsFile=/dev/null -oLogLevel=error'
_ssh="ssh $_sshopts"
_scp="scp $_sshopts"
_imgdir=_work

create_vmhost_manifest () {
    _rel=$1
    _templ=$2
    _img=$3
    _file=$_wdir/${_templ}-${_rel}.yaml
    echo "---" >  $_file
    echo "vms:" >>  $_file
    for i in $(seq 0 $((NUM_NODES - 1))); do
        cat $(dirname $0)/${_templ}-template.yaml \
	    | sed "s/__HOST__/host-$i/" \
	    | sed "s/__IMAGE__/$_img/" >> $_file
	# add NVDIMM emulation related part on workers
	if [ $i -ne 0 ]; then
	    cat >> $_file <<EOF
      - |
        EXTRA_QEMU_OPTS=
        -machine nvdimm=on -object memory-backend-file,id=mem1,share,mem-path=/data/nvdimm0,size=32768M -device nvdimm,memdev=mem1,id=nv1,label-size=2097152
EOF
	fi
    done
}

set_docker_proxy () {
    a=$1
    if [ -n "${http_proxy:-}" ]; then
        if [ -f $_wdir/govm_docker_proxy.$a.done ]; then
	    return 0
        fi
        # proxy for docker
        $_ssh root@$a mkdir -p /etc/systemd/system/docker.service.d/
        $_ssh root@$a "cat >/etc/systemd/system/docker.service.d/proxy.conf" <<EOF
[Service]
Environment="HTTP_PROXY=$http_proxy" "HTTPS_PROXY=$https_proxy" "NO_PROXY=$NO_PROXY"
EOF
        touch $_wdir/govm_docker_proxy.$a.done
    fi
}

set_docker_registry () {
    a=$1
    if [ -f $_wdir/govm_docker_registry.$a.done ]; then
	return 0
    fi
    # configure insecure registry for docker
    $_ssh root@$a "mkdir -p /etc/docker"
    $_ssh root@$a "cat >/etc/docker/daemon.json" <<EOF
{ "insecure-registries": ["10.237.72.78:5000"] }
EOF
    touch $_wdir/govm_docker_registry.$a.done
}

# kubeadm_args is used from multiple functions so we have separate function to set its value
setup_kubeadm_args () {
    kubeadm_args=
# TODO: CRIO mode not tried, not complete/supported
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

}

kubeadm_init () {
    if [ -f $_wdir/govm_kubeadm_init.done ]; then
	return 0
    fi
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

# Needed for flannel (https://clearlinux.org/documentation/clear-linux/tutorials/kubernetes).
kubeadm_config_cluster="$kubeadm_config_cluster
networking:
  podSubnet: \"10.244.0.0/16\""

$_ssh root@$master "cat >/tmp/kubeadm-config.yaml" <<EOF
$kubeadm_config_init
---
$kubeadm_config_kubelet
---
$kubeadm_config_cluster
EOF

# TODO: it is possible to set up each node in parallel, see
# https://kubernetes.io/docs/reference/setup-tools/kubeadm/kubeadm-init/#automating-kubeadm

kubeadm_args_init="$kubeadm_args_init --config=/tmp/kubeadm-config.yaml"
$_ssh root@$master "$PROXY_ENV kubeadm init $kubeadm_args $kubeadm_args_init" | tee $_wdir/kubeadm.log
$_ssh root@$master "mkdir -p .kube"
$_ssh root@$master "cp -i /etc/kubernetes/admin.conf .kube/config"
$_ssh root@$master "$PROXY_ENV kubectl apply -f https://raw.githubusercontent.com/coreos/flannel/master/Documentation/kube-flannel.yml"

# Install addon storage CRDs, needed if certain feature gates are enabled.
# Only applicable to Kubernetes 1.13 and older. 1.14 will have them as builtin APIs.
# Install temporarily for 1.14 as well, until they are not released as sidecars.
if $_ssh root@$master "$PROXY_ENV kubectl version" | grep -q '^Server Version.*Major:"1", Minor:"1[01234]"'; then
    if [[ "$TEST_FEATURE_GATES" == *"CSINodeInfo=true"* ]]; then
        $_ssh root@$master "$PROXY_ENV kubectl create -f https://raw.githubusercontent.com/kubernetes/kubernetes/release-1.14/cluster/addons/storage-crds/csinodeinfo.yaml"
    fi
    if [[ "$TEST_FEATURE_GATES" == *"CSIDriverRegistry=true"* ]]; then
        $_ssh root@$master "$PROXY_ENV kubectl create -f https://raw.githubusercontent.com/kubernetes/kubernetes/release-1.14/cluster/addons/storage-crds/csidriver.yaml"
    fi
fi

touch $_wdir/govm_kubeadm_init.done
}

nodes_join () {
# Let the other machines join the cluster.
for a in $allnodes_ip; do
    if [ "$a" = "$master" ]; then
	continue
    fi
    if [ ! -f $_wdir/govm_join.$a.done ]; then
        $_ssh root@$a $(sed -z 's/\\\n//g' $_wdir/kubeadm.log |grep "kubeadm join.*token" ) $kubeadm_args
        touch $_wdir/govm_join.$a.done
    fi
done
}

copy_kubeconfig_out () {
# get cluster config from master to this host so that kubectl can be used from host
    if [ -f $_wdir/govm_copy_config_out.done ]; then
	return 0
    fi
( echo "Use $(pwd)/$_wdir/kube.config as KUBECONFIG to access the running cluster." ) 2>/dev/null
$_ssh root@$master 'cat /etc/kubernetes/admin.conf' | sed -e "s;https://.*:6443;https://${TEST_IP_ADDR}.2:6443;" >$_wdir/kube.config
    touch $_wdir/govm_copy_config_out.done
}

init_nvdimm_labels () {
    a=$1
    if [ "$a" = "$master" ]; then
	# skip nvdimm ops on master, it does not have PMEM
	return 0
    fi
    if [ -f $_wdir/govm_nvdimm_labels.$a.done ]; then
	return 0
    fi
    $_ssh root@$a "ndctl disable-region region0"
    $_ssh root@$a "ndctl init-labels nmem0"
    $_ssh root@$a "ndctl enable-region region0"
    touch $_wdir/govm_nvdimm_labels.$a.done
}

cluster_setup () {
    if [ -f $_wdir/govm_cluster_setup.done ]; then
	return 0
    fi
    # in clear version we had this deleting label from master.
    # But as we deal with new cluster, and we never add this label to host-0,
    # this deletion is not needed I think. Lets just add label on worker nodes.
    #$_ssh root@$master "kubectl label node host-0 storage-"

    nodes=`govm list -f '{{select (filterRegexp . "Name" "host-*") "Name"}}'`
    for n in $nodes; do
        if [ "$n" = "host-0" ]; then
	    # skip master
	    continue
        fi
        $_ssh root@$master "kubectl label --overwrite node $n storage=pmem"
    done
    # note that we use files under $_wdir/ here
    if ! [ -e $_wdir/govm_secrets.done ] || [ $($_ssh root@$master kubectl get secrets | grep pmem- | wc -l) -ne 2 ]; then
        KUBECTL="$_ssh root@$master kubectl" PATH="$PWD/_work/bin/:$PATH" ./test/setup-ca-kubernetes.sh
	touch $_wdir/govm_secrets.done
    fi
    $_ssh root@$master kubectl version --short | grep 'Server Version' | sed -e 's/.*: v\([0-9]*\)\.\([0-9]*\)\..*/\1.\2/' >$_wdir/kvm-kubernetes.version
    . test/test-config.sh && \
    if ! $_ssh root@$master kubectl get statefulset.apps/pmem-csi-controller daemonset.apps/pmem-csi >/dev/null 2>&1; then
        $_ssh root@$master kubectl create -f - <deploy/kubernetes-$(cat $_wdir/kvm-kubernetes.version)/pmem-csi-${TEST_DEVICEMODE}-testing.yaml
    fi
    if ! $_ssh root@$master kubectl get storageclass/pmem-csi-sc-cache >/dev/null 2>&1; then
        $_ssh root@$master kubectl create -f - <deploy/kubernetes-$(cat $_wdir/kvm-kubernetes.version)/pmem-storageclass-cache.yaml
    fi
    if ! $_ssh root@$master kubectl get storageclass/pmem-csi-sc-ext4 >/dev/null 2>&1; then
        $_ssh root@$master kubectl create -f - <deploy/kubernetes-$(cat $_wdir/kvm-kubernetes.version)/pmem-storageclass-ext4.yaml
    fi
    if ! $_ssh root@$master kubectl get storageclass/pmem-csi-sc-xfs >/dev/null 2>&1; then
        $_ssh root@$master kubectl create -f - <deploy/kubernetes-$(cat $_wdir/kvm-kubernetes.version)/pmem-storageclass-xfs.yaml
    fi
    echo
    echo "The test cluster is ready. Log in with $_ssh root@$master, run kubectl once logged in."
    echo "Alternatively, KUBECONFIG=$PWD/$_wdir/kube.config can also be used directly."
    echo "To try out the pmem-csi driver persistent volumes:"
    echo "   cat deploy/kubernetes-$(cat $_wdir/kvm-kubernetes.version)/pmem-pvc.yaml | $_ssh root@$master kubectl create -f -"
    echo "   cat deploy/kubernetes-$(cat $_wdir/kvm-kubernetes.version)/pmem-app.yaml | $_ssh root@$master kubectl create -f -"
    echo "To try out the pmem-csi driver cache volumes:"
    echo "   cat deploy/kubernetes-$(cat $_wdir/kvm-kubernetes.version)/pmem-pvc-cache.yaml | $_ssh root@$master kubectl create -f -"
    echo "   cat deploy/kubernetes-$(cat $_wdir/kvm-kubernetes.version)/pmem-app-cache.yaml | $_ssh root@$master kubectl create -f -"

    # create ssh helpers
    for i in $(seq 0 $((NUM_NODES - 1))); do
	_helper=$_wdir/ssh.${i}
        a=`govm list |grep host-$i |awk '{print $4}'`
        echo "#!/bin/sh" > $_helper
        echo "exec $_ssh root@$a \"\$@\"" >> $_helper
        chmod 755 $_helper
    done

    touch $_wdir/govm_cluster_setup.done
}

