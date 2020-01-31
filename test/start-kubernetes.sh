#!/bin/bash
set -o errexit
set -o pipefail

TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname $(readlink -f $0))}
source ${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}
TEST_DISTRO=${TEST_DISTRO:-clear}
DEPLOYMENT_SUFFIX=${DEPLOYMENT_SUFFIX:-govm}
CLUSTER=${CLUSTER:-${TEST_DISTRO}-${DEPLOYMENT_SUFFIX}}
DEPLOYMENT_ID=${DEPLOYMENT_ID:-pmem-csi-${CLUSTER}}
GOVM_YAML=${GOVM_YAML:-$(mktemp --suffix $DEPLOYMENT_ID.yml)}
REPO_DIRECTORY=${REPO_DIRECTORY:-$(dirname $TEST_DIRECTORY)}
TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname $(readlink -f $0))}
RESOURCES_DIRECTORY=${RESOURCES_DIRECTORY:-${REPO_DIRECTORY}/_work/resources}
WORKING_DIRECTORY="${WORKING_DIRECTORY:-${REPO_DIRECTORY}/_work/${CLUSTER}}"
LOCKFILE="${LOCKFILE:-${REPO_DIRECTORY}/_work/start-kubernetes.exclusivelock}"
LOCKDELAY="${LOCKDELAY:-300}" # seconds
NODES=(${NODES:-$DEPLOYMENT_ID-master
        $DEPLOYMENT_ID-worker1
        $DEPLOYMENT_ID-worker2
        $DEPLOYMENT_ID-worker3})
CLOUD="${CLOUD:-true}"
FLAVOR="${FLAVOR:-medium}" # actual memory size and CPUs selected below
SSH_KEY="${SSH_KEY:-${RESOURCES_DIRECTORY}/id_rsa}"
SSH_PUBLIC_KEY="${SSH_KEY}.pub"
KVM_CPU_OPTS="${KVM_CPU_OPTS:-\
 -m ${TEST_NORMAL_MEM_SIZE}M,slots=${TEST_MEM_SLOTS},maxmem=$((${TEST_NORMAL_MEM_SIZE} + ${TEST_PMEM_MEM_SIZE}))M -smp ${TEST_NUM_CPUS} \
 -cpu ${TEST_QEMU_CPU} \
 -machine pc,accel=kvm,nvdimm=on}"
EXTRA_QEMU_OPTS="${EXTRA_QWEMU_OPTS:-\
 -object memory-backend-file,id=mem1,share=${TEST_PMEM_SHARE},\
mem-path=/data/nvdimm0,size=${TEST_PMEM_MEM_SIZE}M \
 -device nvdimm,id=nvdimm1,memdev=mem1,label-size=${TEST_PMEM_LABEL_SIZE} \
}"
INIT_CLUSTER=${INIT_CLUSTER:-true}
TEST_INIT_REGION=${TEST_INIT_REGION:-true}

# Set distro-specific defaults.
case ${TEST_DISTRO} in
    clear)
        CLOUD_USER=${CLOUD_USER:-clear}
        EFI=${EFI:-true}
        # Either TEST_DISTRO_VERSION or TEST_CLEAR_LINUX_VERSION (for backwards compatibility)
        # can be set.
        TEST_DISTRO_VERSION=${TEST_DISTRO_VERSION:-${TEST_CLEAR_LINUX_VERSION}}
        if [ "$TEST_DISTRO_VERSION" ]; then
            # We used to use cloud.img in the past and switched to cloudguest.img
            # with Clear Linux 29920 because that version only listed cloudguest
            # in "latest-images". Same content, just a different name.
            # Using cloudguest.img seems more likely to work in the future.
            if [ "$TEST_DISTRO_VERSION" -ge 29920 ]; then
                CLOUD_IMAGE="clear-$TEST_DISTRO_VERSION-cloudguest.img.xz"
            else
                CLOUD_IMAGE="clear-$TEST_DISTRO_VERSION-cloud.img.xz"
            fi
        else
            # Either cloud.img or cloudguest.img is fine, should have the same content.
            CLOUD_IMAGE=${CLOUD_IMAGE:-$(\
                       curl -s https://download.clearlinux.org/image/latest-images |
                           awk '/cloud.img|cloudguest.img/ {print $0}' |
                           head -n1)}
        fi
        IMAGE_URL=${IMAGE_URL:-https://download.clearlinux.org/releases/${CLOUD_IMAGE//[!0-9]/}/clear}
        ;;
    fedora)
        CLOUD_USER=${CLOUD_USER:-fedora}
        EFI=${EFI:-false}
        TEST_DISTRO_VERSION=${TEST_DISTRO_VERSION:-31}
        FEDORA_CLOUDIMG_REL=${FEDORA_CLOUDIMG_REL:-1.9}
        IMAGE_URL=${IMAGE_URL:-https://download.fedoraproject.org/pub/fedora/linux/releases/${TEST_DISTRO_VERSION}/Cloud/x86_64/images}
        CLOUD_IMAGE=${CLOUD_IMAGE:-Fedora-Cloud-Base-${TEST_DISTRO_VERSION}-${FEDORA_CLOUDIMG_REL}.x86_64.raw.xz}
        ;;
esac

SSH_TIMEOUT=120
SSH_ARGS="-oIdentitiesOnly=yes -oStrictHostKeyChecking=no \
        -oUserKnownHostsFile=/dev/null -oLogLevel=error \
        -i ${SSH_KEY}"
SSH_ARGS+=" -oServerAliveInterval=15" # required for disruptive testing, to detect quickly when the machine died
TEST_CHECK_SIGNED_FILES=${TEST_CHECK_SIGNED_FILES:-true}

function die() {
    echo >&2 "ERROR: $@"
    exit 1
}

function download() {
    # Sometimes we just have a temporary connection issue or bad mirror, try repeatedly.
    echo >&2 "Downloading ${IMAGE_URL}/${CLOUD_IMAGE} image"
    local cnt=0
    while ! curl --fail --location --output "${CLOUD_IMAGE}" "${IMAGE_URL}/${CLOUD_IMAGE}"; do
        if [ $cnt -ge 5 ]; then
            die "Download failed repeatedly, giving up."
        fi
        cnt=$(($cnt + 1))
    done
}

function download_image() (
    # If we start multiple clusters in parallel, we must ensure that only one
    # process downloads the shared image
    flock -x -w $LOCKDELAY 200

    cd $RESOURCES_DIRECTORY
    if [ -e "${CLOUD_IMAGE/.xz}" ]; then
        CLOUD_IMAGE=${CLOUD_IMAGE/.xz}
        echo >&2 "$CLOUD_IMAGE found, skipping download"
    else
        case $TEST_DISTRO in
            clear)
                download
                if $TEST_CHECK_SIGNED_FILES; then
                    curl -s -O "${IMAGE_URL}/${CLOUD_IMAGE}-SHA512SUMS" || die "failed to download ${IMAGE_URL}/${CLOUD_IMAGE}-SHA512SUMS"
                    curl -s -O "${IMAGE_URL}/${CLOUD_IMAGE}-SHA512SUMS.sig" || die "failed to download ${IMAGE_URL}/${CLOUD_IMAGE}-SHA512SUMS.sig"
                    curl -s -O "${IMAGE_URL}/ClearLinuxRoot.pem" || die "failed to download ${IMAGE_URL}/ClearLinuxRoot.pem"
                    if ! openssl smime -verify \
                         -in "${CLOUD_IMAGE}-SHA512SUMS.sig" \
                         -inform DER \
                         -content "${CLOUD_IMAGE}-SHA512SUMS" \
                         -CAfile "ClearLinuxRoot.pem"; then
                        cat >&2 <<EOF
Image verification failed, see error above.

"unsupported certificate purpose" is a known issue caused by an incompatible openssl
version (https://github.com/clearlinux/distribution/issues/85). Other errors might indicate
a download error or man-in-the-middle attack.

To skip image verification run:
TEST_CHECK_SIGNED_FILES=false make start
EOF
                        exit 2
                    fi
                fi
                ;;
            fedora)
                download
                # TODO: verify image - https://alt.fedoraproject.org/en/verify.html
                ;;
            *) die "unsupported TEST_DISTRO=${TEST_DISTRO}";;
        esac
        unxz ${CLOUD_IMAGE} || die "failed to unpack ${CLOUD_IMAGE}"
        CLOUD_IMAGE=${CLOUD_IMAGE/.xz}

        # We need a qcow image, otherwise copy-on-write does not work
        # (https://github.com/govm-project/govm/blob/08f276f574f9ad6cad29f7c8fde070a4eb542b06/startvm#L25-L29)
        # and the different machines conflict with each other. We cannot call qemu-img directly (might not be
        # installed), but we can use docker and the https://hub.docker.com/r/govm/govm image because we depend
        # on both already.
        if ! file ${CLOUD_IMAGE} | grep -q "QEMU QCOW"; then
            docker run --rm --volume `pwd`:/resources --entrypoint /usr/bin/qemu-img govm/govm -- convert -O qcow2 /resources/${CLOUD_IMAGE} /resources/${CLOUD_IMAGE}.tmp && mv ${CLOUD_IMAGE}.tmp ${CLOUD_IMAGE} || die "conversion to qcow2 format failed"
        fi
    fi

    echo "$CLOUD_IMAGE"
) 200>$LOCKFILE


function print_govm_yaml() (
    if [ "$TEST_ETCD_VOLUME" ]; then
        data_path=$(echo "$TEST_ETCD_VOLUME" | sed -e "s;^$WORKING_DIRECTORY/data/$DEPLOYMENT_ID-master/;/data/;")
        [ "$data_path" != "$TEST_ETCD_VOLUME" ] || die "TEST_ETCD_VOLUME=$TEST_ETCD_VOLUME must be inside $WORKING_DIRECTORY/data/$DEPLOYMENT_ID-master"
    fi

    cat  <<EOF
---
vms:
EOF

    for node in ${NODES[@]}; do
        # Create VM only if its not found in govm list
        if [ -n "$(govm list -f '{{select (filterRegexp . "Name" "^'${node}'$") "Name"}}')" ]; then
            continue
        fi
        cat <<EOF
  - name: ${node}
    image: ${RESOURCES_DIRECTORY}/${CLOUD_IMAGE}
    cloud: ${CLOUD}
    flavor: ${FLAVOR}
    workdir: ${WORKING_DIRECTORY}
    sshkey: ${SSH_PUBLIC_KEY}
    efi: ${EFI}
    ContainerEnvVars:
      - |
        KVM_CPU_OPTS=
        ${KVM_CPU_OPTS}
      - |
        EXTRA_QEMU_OPTS=
        ${EXTRA_QEMU_OPTS} $(if [[ "$node" =~ -master$ ]] && [ "$TEST_ETCD_VOLUME" ]; then echo "-drive file=$data_path,if=virtio,format=raw"; fi )
EOF
    done
)

function node_filter(){
    IFS="|"; echo "($*)"
}

function print_ips() (
    govm list -f '{{select (filterRegexp . "Name" "^'$(node_filter ${NODES[@]})'$") "IP"}}' | tac
)

function extend_no_proxy() (
    for ip in $(print_ips); do
        NO_PROXY+=",$ip"
    done
    echo "$NO_PROXY"
)

function create_vms() (
    setup_script="setup-${TEST_DISTRO}-govm.sh"
    STOP_VMS_SCRIPT="${WORKING_DIRECTORY}/stop.sh"
    RESTART_VMS_SCRIPT="${WORKING_DIRECTORY}/restart.sh"
    print_govm_yaml >$GOVM_YAML || die "failed to create $GOVM_YAML"
    govm compose -f ${GOVM_YAML} || die "govm failed"
    IPS=$(print_ips)

    #Create scripts to delete virtual machines
    (
        machines=$(govm list -f '{{select (filterRegexp . "Name" "^'$(node_filter ${NODES[@]})'$") "Name"}}') &&
        cat <<EOF
#!/bin/bash -e
$(for i in $machines; do echo govm remove $i; done)
EOF
    ) > $STOP_VMS_SCRIPT && chmod +x $STOP_VMS_SCRIPT || die "failed to create $STOP_VMS_SCRIPT"

    #Create script to restart virtual machines
    ( cat <<EOF
#!/bin/bash
num_nodes=$(echo ${IPS} | wc -w)
echo "Rebooting \$num_nodes virtual machines"
for ip in $(echo ${IPS}); do
    ssh $SSH_ARGS ${CLOUD_USER}@\${ip} "sudo systemctl reboot"
done
echo "Waiting for ssh connectivity"
for ip in $(echo ${IPS}); do
    while ! ssh $SSH_ARGS ${CLOUD_USER}@\${ip} exit 2>/dev/null; do
        if [ "\$SECONDS" -gt "$SSH_TIMEOUT" ]; then
            echo "Timeout accessing through ssh"
            exit 1
        fi
    done
done
echo "Waiting for Kubernetes nodes to be ready"
while [ \$(${WORKING_DIRECTORY}/ssh-${CLUSTER} "kubectl get nodes  -o go-template --template='{{range .items}}{{range .status.conditions }}{{if eq .type \"Ready\"}} {{if eq .status \"True\"}}{{printf \"%s\n\" .reason}}{{end}}{{end}}{{end}}{{end}}'" 2>/dev/null | wc -l) -ne \$num_nodes ]; do
    if [ "\$SECONDS" -gt "$SSH_TIMEOUT" ]; then
        echo "Timeout for nodes: ${WORKING_DIRECTORY}/ssh-${CLUSTER} kubectl get nodes:"
        ${WORKING_DIRECTORY}/ssh-${CLUSTER} kubectl get nodes
        exit 1
    fi
    sleep 3
done
EOF
      ) >$RESTART_VMS_SCRIPT && chmod +x $RESTART_VMS_SCRIPT || die "failed to create $RESTART_VMS_SCRIPT"

    vm_id=0
    pids=""
    # Intentional separate loop for first-time connectivity after VM boot-up.
    # In Fedora-31 case it is seen that sshd gets disabled again for short time
    # soon after boot to perform sshd-keygen. Doing initial wait in same loop
    # increases the risk to hit this window  on the master node, causing first
    # actual ssh commands to fail.
    # Doing wait in separate loop adds 2..3 seconds delay on master
    # composed of waiting for initial connectivity of other nodes, before
    # we start connecting to master again (in the 2nd loop below).
    for ip in ${IPS}; do
        SECONDS=0
        #Wait for the ssh connectivity in the vms
        echo "Waiting for ssh connectivity on vm with ip $ip"
        while ! ssh $SSH_ARGS ${CLOUD_USER}@${ip} exit 2>/dev/null; do
            if [ "$SECONDS" -gt "$SSH_TIMEOUT" ]; then
                die "timeout accessing ${ip} through ssh"
            fi
        done
    done

    for ip in ${IPS}; do
        NO_PROXY+=",$ip"
        vm_name=$(govm list -f '{{select (filterRegexp . "IP" "'${ip}'") "Name"}}') || die "failed to find VM for IP $ip"
        log_name=${WORKING_DIRECTORY}/${vm_name}.log
        ssh_script=${WORKING_DIRECTORY}/ssh.${vm_id}
        ((vm_id=vm_id+1))
        if [[ "$vm_name" = *"worker"* ]]; then
            workers_ip+="$ip "
        else
            ( cat <<EOF
#!/bin/sh

exec ssh $SSH_ARGS ${CLOUD_USER}@${ip} "\$@"
EOF
            ) >${WORKING_DIRECTORY}/ssh-${CLUSTER} && chmod +x ${WORKING_DIRECTORY}/ssh-${CLUSTER} || die "failed to create ${WORKING_DIRECTORY}/ssh-${CLUSTER}"
        fi
        ( cat <<EOF
#!/bin/sh

exec ssh $SSH_ARGS ${CLOUD_USER}@${ip} "\$@"
EOF
        ) >$ssh_script && chmod +x $ssh_script || die "failed to create $ssh_script"
        ENV_VARS="env$(env_vars) HOSTNAME='$vm_name' IPADDR='$ip'"
        ENV_VARS+=" INIT_KUBERNETES=${INIT_CLUSTER}"
        # The local registry and the master node might be used as insecure registries, enabled that just in case.
        ENV_VARS+=" INSECURE_REGISTRIES='$TEST_INSECURE_REGISTRIES $TEST_LOCAL_REGISTRY pmem-csi-$CLUSTER-master:5000'"
        scp $SSH_ARGS ${TEST_DIRECTORY}/${setup_script} ${CLOUD_USER}@${ip}:. >/dev/null || die "failed to copy install scripts to $vm_name = $ip"
        ssh $SSH_ARGS ${CLOUD_USER}@${ip} "sudo env $ENV_VARS ./$setup_script" </dev/null &> >(log_lines "$vm_name" "$log_name") &
        pids+=" $!"
    done
    waitall $pids || die "at least one of the nodes failed"
)

# Wait for some pids to finish. Once the first one fails, the others
# are killed. Slightly racy, because we also kill the process we already
# waited for and whose pid thus might have been reused.
function waitall() {
    result=0
    numpids=$#
    while [ $numpids -gt 0 ]; do
        if ! wait -n "$@"; then
            if [ $result -eq 0 ]; then
                result=1
                # To increase the chance that the asynchronously logged output is shown first, wait a bit.
                sleep 10
                kill "$@" 2>/dev/null || true # We don't care about errors here, at least one process is gone.
                echo >&2 "ERROR: some child process failed, killed the rest."
            fi
        fi
        # We don't know which one terminated. We just decrement and
        # keep on waiting for all pids.
        numpids=$(($numpids - 1))
    done

    return $result
}

# Prints a single line of foo=<value of foo> assignments for
# proxy variables and all variables starting with TEST_.
function env_vars() (
    for var in $(set | grep -e '^[a-zA-Z_]*=' | sed -e 's/=.*//' | grep -e '^HTTP_PROXY$' -e '^HTTPS_PROXY$' -e '^NO_PROXY$' -e '^TEST_'); do
        echo -n " $var='${!var}'"
    done
)

function log_lines(){
    local prefix="$1"
    local logfile="$2"
    local line
    while read -r line; do
        # swupd output contains carriage returns. We need to filter
        # those out, otherwise the line overwrites the prefix.
        echo "$(date +%H:%M:%S) $prefix: $line" | sed -e 's/\r//' | tee -a $logfile
    done
    echo "$(date +%H:%M:%S) $prefix: END OF OUTPUT"
}

function init_kubernetes_cluster() (
    # Do nothing if INIT_CLUSTER set to false
    ${INIT_CLUSTER} || return 0

    workers_ip=""
    master_ip="$(govm list -f '{{select (filterRegexp . "Name" "^'${DEPLOYMENT_ID}'-master$") "IP"}}')" || die "failed to find master IP"
    join_token=""
    install_k8s_script="setup-kubernetes.sh"
    KUBECONFIG=${WORKING_DIRECTORY}/kube.config
    echo "Installing dependencies on cloud images, this process may take some minutes"
    vm_id=0
    pids=""
    IPS=$(print_ips)
    # in Fedora-31 case, add a flag to kernel cmdline and reboot
    if [ "$TEST_DISTRO" = "fedora" -a "$TEST_DISTRO_VERSION" = "31" ]; then
        SYSTEMD_UNIFIED_CGROUP_HIERARCHY=systemd.unified_cgroup_hierarchy
        for ip in ${IPS}; do
            ssh $SSH_ARGS ${CLOUD_USER}@${ip} "sudo sed -i 's/n8\"/n8 ${SYSTEMD_UNIFIED_CGROUP_HIERARCHY}=0\"/g' /etc/default/grub"
            ssh $SSH_ARGS ${CLOUD_USER}@${ip} "sudo grub2-mkconfig -o /boot/grub2/grub.cfg"
            # use --force to speed up shutdown as nothing important running yet to wait for
            ssh $SSH_ARGS ${CLOUD_USER}@${ip} "sudo systemctl --force reboot"
        done
        for ip in ${IPS}; do
            SECONDS=0
            echo "After reboot: Waiting for ssh connectivity on vm with ip $ip"
            # Verify that system runs with added cmdline parameter
            while ! ssh $SSH_ARGS ${CLOUD_USER}@${ip} "grep -q ${SYSTEMD_UNIFIED_CGROUP_HIERARCHY} /proc/cmdline"; do
                if [ "$SECONDS" -gt "$SSH_TIMEOUT" ]; then
                    die "timeout waiting for ${ip} to reboot with changed kernel parameters"
                fi
            done
        done
    fi
    for ip in ${IPS}; do
        vm_name=$(govm list -f '{{select (filterRegexp . "IP" "'${ip}'") "Name"}}') || die "failed to find VM for IP $ip"
        log_name=${WORKING_DIRECTORY}/${vm_name}.log
        ssh_script=${WORKING_DIRECTORY}/ssh.${vm_id}
        ((vm_id=vm_id+1))
        if [[ "$vm_name" = *"worker"* ]]; then
            workers_ip+="$ip "
        fi
        ENV_VARS="env$(env_vars) HOSTNAME='$vm_name' IPADDR='$ip'"
        scp $SSH_ARGS ${TEST_DIRECTORY}/
        scp $SSH_ARGS ${REPO_DIRECTORY}/_work/pmem-ca/ca.pem ${CLOUD_USER}@${ip}:ca.crt
        scp $SSH_ARGS ${TEST_DIRECTORY}/${install_k8s_script} ${CLOUD_USER}@${ip}:. >/dev/null || die "failed to copy install scripts to $vm_name = $ip"
        ssh $SSH_ARGS ${CLOUD_USER}@${ip} "env $ENV_VARS ./$install_k8s_script" </dev/null &> >(log_lines "$vm_name" "$log_name") &
        pids+=" $!"
    done
    waitall $pids || die "at least one of the nodes failed"
    #get kubeconfig
    scp $SSH_ARGS ${CLOUD_USER}@${master_ip}:.kube/config $KUBECONFIG || die "failed to copy Kubernetes config file"
    export KUBECONFIG=${KUBECONFIG}

    #get kubernetes join token
    join_token=$(ssh $SSH_ARGS ${CLOUD_USER}@${master_ip} "$ENV_VARS kubeadm token create --print-join-command") || die "could not get kubeadm join token"
    pids=""
    for ip in ${workers_ip}; do

        vm_name=$(govm list -f '{{select (filterRegexp . "IP" "'${ip}'") "Name"}}') || die "could not find VM name for $ip"
        log_name=${WORKING_DIRECTORY}/${vm_name}.log
        ( ssh $SSH_ARGS ${CLOUD_USER}@${ip} "set -x; $ENV_VARS sudo ${join_token/kubeadm/kubeadm --ignore-preflight-errors=SystemVerification}" &&
          ssh $SSH_ARGS ${CLOUD_USER}@${master_ip} "set -x; kubectl label --overwrite node $vm_name storage=pmem" ) </dev/null &> >(log_lines "$vm_name" "$log_name") &
        pids+=" $!"
    done
    waitall $pids || die "at least one worker failed to join the cluster"
)

function init_workdir() (
    mkdir -p $WORKING_DIRECTORY || die "failed to create $WORKING_DIRECTORY"
    mkdir -p $RESOURCES_DIRECTORY || die "failed to create $RESOURCES_DIRECTORY"
    (
        flock -x -w $LOCKDELAY 200
        if [ ! -e  "$SSH_KEY" ]; then
            ssh-keygen -N '' -f ${SSH_KEY} >/dev/null || die "failed to create ${SSH_KEY}"
        fi
    ) 200>$LOCKFILE
)

function init_pmem_regions() {
    if $TEST_INIT_REGION; then
        for vm_id in ${!NODES[@]}; do
            ${WORKING_DIRECTORY}/ssh.${vm_id} sudo ndctl disable-region region0
            ${WORKING_DIRECTORY}/ssh.${vm_id} sudo ndctl init-labels nmem0
            ${WORKING_DIRECTORY}/ssh.${vm_id} sudo ndctl enable-region region0
        done
    fi
}

function check_status() { # intentionally a composite command, so "exit" will exit the main script
    if ${INIT_CLUSTER} ; then
        deployments=$(govm list -f '{{select (filterRegexp . "Name" "^'${DEPLOYMENT_ID}'-master$") "Name"}}')
        if [ ! -z "$deployments" ]; then
            echo "Kubernetes cluster ${CLUSTER} is already running, using it unchanged."
            exit 0
        fi
    else 
        vm_count=$(govm list -f '{{select (filterRegexp . "Name" "^'$(node_filter ${NODES[@]})'$") "Name"}}' | wc -l)
        if [ $vm_count == ${#NODES[@]} ]; then
            echo "All needed nodes are already running, using them unchanged."
            exit 0
        fi
    fi

    if ${TEST_CHECK_KVM} && [ ! -e /dev/kvm ]; then
        echo "ERROR: /dev/kvm does not exist. KVM has to be enabled before starting the virtual cluster."
        exit 1
    fi
}

FAILED=true
function cleanup() (
    if $FAILED; then
        set +xe
        echo "Cluster creation failed."
        echo "govm status:"
        govm list
        echo "Docker status:"
        docker ps
        # Pick one restarting container from the current govm cluster and dump its log, if one exists.
        # We only need one log, other containers are probably failing for the same reason.
        restarting=$(for i in ${NODES}; do
                        docker ps --filter status=restarting --filter name=govm.$(id -u -n).$i --format '{{.ID}}'
                    done | head -n 1)
        if [ "$restarting" ]; then
            name=$(docker ps --filter id=$restarting --format '${{.Names}}')
            echo "Docker container $name is restarting, here's why (docker log):"
            docker logs $restarting | sed -e "s/^/$name: /"
        else
            # Fall back to master node if nothing was restarting.
            name=govm.$(id -u -n).${NODES[0]}
            echo "Docker log for $name:"
            docker logs $name | sed -e "s/^/$name: /"
        fi
        for vm in $(govm list -f '{{select (filterRegexp . "Name" "^'$(node_filter ${NODES[@]})'$") "Name"}}'); do
            govm remove "$vm"
        done
        rm -rf $WORKING_DIRECTORY
    fi
)

check_status # exits if nothing to do
trap cleanup EXIT

if init_workdir &&
   CLOUD_IMAGE=$(download_image) &&
   create_vms &&
   NO_PROXY=$(extend_no_proxy) &&
   init_pmem_regions &&
   init_kubernetes_cluster; then
    FAILED=false
else
    exit 1
fi
