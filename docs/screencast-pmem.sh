#!/bin/bash 
#
# Instructions on how to use.
#
# Everything was verified on Ubuntu 20.04 with 5.11 Kernel.
#
# PREREQUISITES
#
# 1) Use a Putty terminal size of 128 x 39 characters minimum 
# 2) Make sure AppDirect mode is already enabled using the ipmctl utility
# 3) Setup a basic Kubernetes single node cluster with the master untainted. Kubespray is recommended.
# 4) Install Operator Manager from https://sdk.operatorframework.io/docs/installation/
# 
# CUSTOMIZATIONS
#
# 1) Edit the prompt() function to change the font look and feel including the color.
# 2) In corresponding functions, edit the num_row_move_cursor to control cursor position up for arrow.
# 3) Edit the num_col_move_cursor to control cursor position from left. This is only needed if not aligned.
# 4) Edit the command() and out() function to modify the speed variable to control how fast the typing is.
#
# RECORD A SCREENCAST
#
# 1) Initialize the yaml files with "./screencast-pmem.sh generateyamls".
# 2) Record screencast with "./screencast-pmem.sh record" which records to pmem-csi.cast.
# 
# CLEANUP
# This removes NFD labels, kubernetes objects, and PMem volumes and namespaces.
#
# 1) Cleanup everything with "./screencast-pmem.sh cleanup"
#

PV='pv -qL'

prompt()
{
  # Set a color prompt to better mimic a Linux terminal. 
  /bin/echo -n -e "\n\e[1;32m[PMem@PMEM-CSI]$\e[0m "
  # This is used for the cursor positioning of ansi escape characters
  # Add the total number of characters in prompt + 1
  prompt_col_start_position=17
}

command()
{
  speed=$2
  [ -z "$speed" ] && speed=20
  echo  "$1" | $PV $speed
  sh -c "$1"
  prompt
}

out()
{
  speed=$2
  [ -z "$speed" ] && speed=20
  echo -n "$1" | $PV $speed
  prompt
}

record()
{
  clear
  out 'Record this screencast'
  command 'asciinema rec -t "Intel® Optane™ Persistent Memory PMEM-CSI storage driver installation demo" -c "./screencast-pmem.sh play"  pmem-csi.cast'

}

screen1()  #Introduction
{
  clear 
  prompt
  out "#  These instructions show how to use persistent memory within Kubernetes."
  out "#  This example is using Intel® Optane™ Persistent Memory configured in a 2-2-2 configuration."
  out "#  The memory has already been configured for AppDirect mode using the ipmctl utility."
  out "#  This example will use the default lvm mode instead of direct for the storage interface."
  out ''
  sleep 1
  out "#  Use ipmctl to see how much persistent memory has been configured for AppDirect mode."
  out ''
  sleep 1
  command 'sudo ipmctl show -memoryresources'
  sleep 1
  # Use ANSI escape characters to move cursor up and over to show users the AppDirect line.
  num_rows_move_cursor=5
  num_cols_move_cursor=45
  printf "\033[${num_rows_move_cursor}A\033[${num_cols_move_cursor}C"
  printf "\x1b[31m<========= 1512 GiB of persistent memory\\x1b[0m\n"
  # Move the cursor back to where it would start again
  printf "\033[$(expr ${num_rows_move_cursor} - 1)B\033[${prompt_col_start_position}C"
  sleep 3
}

screen2() # Install Node Feature Discovery
{
  clear
  prompt
  out '#  This demo is being run on a single node master that is untainted. First, install'
  out '#  Node Feature Discovery (NFD) to automatically detect detailed hardware features of each node'
  out '#  and label each node with those features. If a node supports persistent memory then NFD'
  out '#  will add a label "feature.node.kubernetes.io/memory-nv.dax": "true" to each supported node.'
  out ''
  sleep 1
  out '#  Check what the current labels are before NFD.'
  out ''
  sleep 1
  command 'kubectl get no -o json | jq .items[].metadata.labels'
  sleep 2
  out '#  Nothing is labeled yet for persistent memory.'
  out ''
  sleep 1
  out '#  Now apply NFD.'
  out ''
  sleep 1
  command 'kubectl apply -k https://github.com/kubernetes-sigs/node-feature-discovery/deployment/overlays/default?ref=v0.9.0'
  sleep 1
  command 'kubectl wait --for condition=available deployment/nfd-master -n node-feature-discovery'
  sleep 5
  command 'kubectl get no -o json | jq .items[].metadata.labels'
  sleep 2
  # Use ANSI escape characters to move cursor up and over to show users the 
  # nv.present label. This may change depending on which NFD features are found.
  # Adjust these numbers based on what NFD discovers.
  num_rows_move_cursor=18
  num_cols_move_cursor=45
  printf "\033[${num_rows_move_cursor}A\033[${num_cols_move_cursor}C"
  printf "\x1b[31m<========= Persistent Memory was found and labeled by NFD\\x1b[0m\n"
  # Move the cursor back to where it would start again.
  printf "\033[$(expr ${num_rows_move_cursor} - 1)B\033[${prompt_col_start_position}C"
  sleep 3
}

screen3() # Install PMEM-CSI operator from operator hub
{
  clear
  prompt
  out '#  Now install the PMEM-CSI operator. This can be done either by using the yaml files on'
  out '#  the PMEM-CSI GitHub or by using the Operator Hub. If you are using OpenShift, then the'
  out '#  recommended way is to use the RedHat Catalog.'
  out ''
  sleep 1
  out '#  On Kubernetes < 1.21, additional work is needed to configure the scheduler extender and'
  out '#  webhook as explained in the documentation on the PMEM-CSI GitHub.'
  out ''
  sleep 1
  out '#  This cluster has the Operator Hub installed, so install using that method.'
  out ''
  sleep 1
  command 'kubectl get pods -n olm'
  sleep 2
  out '#  Now search for the PMEM-CSI operator.'
  out ''
  sleep 1
  command 'kubectl get packagemanifest | grep pmem-csi'
  sleep 2
  out '#  To install an Operator you need to:'
  out '#  1) Create the "pmem-csi" namespace if it does not exist.'
  out ''
  sleep 1
  command 'kubectl create ns pmem-csi'
  sleep 2
  out '#  2) Create an OperatorGroup.'
  out ''
  sleep 1
  command 'cat og-pmem-csi.yaml'
  sleep 2
  command 'kubectl apply -f og-pmem-csi.yaml'
  sleep 2
  out '#  3) Create a Subscription for the operator.'
  out ''
  sleep 1
  command 'cat subscription-pmem-csi.yaml'
  sleep 2
  command 'kubectl apply -f subscription-pmem-csi.yaml'
  sleep 5
  command 'kubectl wait --for condition=available deployment/pmem-csi-operator -n pmem-csi'
  sleep 1
  out '#  Verify the operator was installed and is running.'
  out ''
  sleep 1
  command 'kubectl get pods -n pmem-csi'
  sleep 2
  out '#  Success!'
  sleep 3
}

screen4() # Create pmemcsideployment
{
  clear
  prompt
  out '#  Now a driver deployment needs to be created using a pmemcsideployment crd.'
  out ''
  sleep 1
  command 'cat pmemcsideployment.yaml'
  sleep 2
  command 'kubectl apply -f pmemcsideployment.yaml'
  sleep 2
  command 'kubectl get pmemcsideployment'
  sleep 2
  out '#  Check to see if the controller and node pods were created.'
  out ''
  sleep 1
  command 'kubectl wait --for condition=available deployment/pmem-csi-intel-com-controller -n pmem-csi'
  sleep 2
  command 'kubectl get pods -n pmem-csi'
  sleep 2
  out '#  GREAT! Now the driver is running and ready to create persistent volumes.'
  sleep 3
}

screen5()  # Create a StorageClass and PVC
{
  clear
  prompt
  out '#  Next, a storage class needs to be created. This one uses a volumeBindingMode'
  out '#  called WaitForFirstConsumer which means that volumes will not be created'
  out '#  until a pod really needs them. This is better for pod scheduling.'
  out ''
  sleep 1
  command 'curl -L https://github.com/intel/pmem-csi/raw/v1.0.1/deploy/common/pmem-storageclass-late-binding.yaml'
  sleep 2
  command 'kubectl apply -f https://github.com/intel/pmem-csi/raw/v1.0.1/deploy/common/pmem-storageclass-late-binding.yaml'
  sleep 2
  out '#  Create a PersistentVolumeClaim for that StorageClass.'
  out ''
  sleep 1
  command 'curl -L https://github.com/intel/pmem-csi/raw/v1.0.1/deploy/common/pmem-pvc-late-binding.yaml'
  sleep 2
  command 'kubectl apply -f https://github.com/intel/pmem-csi/raw/v1.0.1/deploy/common/pmem-pvc-late-binding.yaml'
  sleep 2
  out '#  If you describe the PVC, you will see the volume is still waiting for its first consumer.'
  out ''
  sleep 1
  command 'kubectl describe pvc/pmem-csi-pvc-late-binding'
  sleep 3
}

screen6()  # Start pod app and show users that PMem is being used
{
  clear
  prompt
  out '#  To check that everything is working properly, deploy a pod that will mount persistent memory'
  out '#  to the /data partition of the container.'
  out ''
  sleep 1
  command 'curl -L https://github.com/intel/pmem-csi/raw/v1.0.1/deploy/common/pmem-app-late-binding.yaml'
  sleep 2
  command 'kubectl apply -f https://github.com/intel/pmem-csi/raw/v1.0.1/deploy/common/pmem-app-late-binding.yaml'
  sleep 2
  command 'kubectl wait --for condition=ready pod/my-csi-app'
  sleep 1
  command 'kubectl get pods'
  sleep 2
  out '#  Inside this pod there is an application called pmem-dax-check which can verify whether'
  out '#  MAP_SYNC is supported. If that works, then it confirms that the file is'
  out '#  stored on persistent memory.'
  out ''
  sleep 1
  out '#  Add some random data to /data which should be a persistent memory volumeMount'
  out '#  and also to the /home folder which is not persistent memory.'
  out ''
  sleep 1
  command 'kubectl exec my-csi-app -- dd if=/dev/urandom of=/home/notpmem.txt bs=1M count=16'
  sleep 2
  command 'kubectl exec my-csi-app -- dd if=/dev/urandom of=/data/pmem.txt bs=1M count=16'
  sleep 2
  out '# Check if MAP_SYNC is supported on files within both folders.'
  out ''
  sleep 1
  command 'kubectl exec my-csi-app -- pmem-dax-check /home/notpmem.txt'
  sleep 2
  out '#  This is not persistent memory.'
  out ''
  sleep 1
  command 'kubectl exec my-csi-app -- pmem-dax-check /data/pmem.txt'
  sleep 2
  out '#  SUCCESS! MAP_SYNC is supported which means this was read from persistent memory.'
  sleep 3
}

screen7()  # Output of ndctl and pvdisplay for users
{
  clear
  prompt
  out '#  Take a look at the system to see the namespaces and logical volumes that the'
  out '#  PMEM-CSI driver created. Use the ndctl utility to see the new namespaces.'
  out ''
  sleep 1
  command 'sudo ndctl list'
  sleep 2
  command 'sudo ndctl list -RuN'
  sleep 2
  out '#  If you recall, when the PVC was created it allocated 4 GiB. Use lvdisplay to check the output.'
  out ''
  sleep 1
  command 'sudo lvdisplay'
  sleep 1
  # Use ANSI escape characters to move cursor up and over to show users the 4 GiB.
  # Adjust these numbers if other logical volumes on the system affect the positioning.
  num_rows_move_cursor=9
  num_cols_move_cursor=20
  printf "\033[${num_rows_move_cursor}A\033[${num_cols_move_cursor}C"
  printf "\x1b[31m<========= 4 GiB matches the PV\\x1b[0m\n"
  # Move the cursor back to where it would start again
  printf "\033[$(expr ${num_rows_move_cursor} - 1)B\033[${prompt_col_start_position}C"
  sleep 3
}

screen8()
{
  clear
  prompt
  out '#  As you can see, using persistent memory in your cluster is easy to do when using the'
  out '#  PMEM-CSI operator. Read through the PMEM-CSI GitHub to see the different ways that'
  out '#  the driver can be configured to support different types of storage methodologies and'
  out '#  other configuration options.'
}

cleanupeverything()
{
kubectl delete -f https://github.com/intel/pmem-csi/raw/v1.0.1/deploy/common/pmem-app-late-binding.yaml
kubectl delete -f https://github.com/intel/pmem-csi/raw/v1.0.1/deploy/common/pmem-pvc-late-binding.yaml
kubectl delete -f https://github.com/intel/pmem-csi/raw/v1.0.1/deploy/common/pmem-storageclass-late-binding.yaml
kubectl delete -f pmemcsideployment.yaml
kubectl delete crd pmemcsideployments.pmem-csi.intel.com
kubectl delete -f subscription-pmem-csi.yaml
kubectl delete -f og-pmem-csi.yaml
kubectl delete ns pmem-csi
kubectl delete -k https://github.com/kubernetes-sigs/node-feature-discovery/deployment/overlays/default?ref=v0.9.0
kubectl get pods -A

sudo vgdisplay
sudo vgremove -f ndbus0region0fsdax
sudo vgremove -f ndbus0region1fsdax
sudo pvdisplay
sudo pvremove /dev/pmem0
sudo pvremove /dev/pmem1
sudo ndctl disable-namespace namespace1.0
sudo ndctl destroy-namespace namespace1.0 --force
sudo ndctl disable-namespace namespace0.0
sudo ndctl destroy-namespace namespace0.0 --force
sudo ndctl list

kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.ADX-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.AESNI-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.AVX-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.AVX2-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.AVX512BW-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.AVX512CD-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.AVX512DQ-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.AVX512F-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.AVX512VL-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.AVX512VNNI-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.FMA3-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.IBPB-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.MPX-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.STIBP-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cpuid.VMX-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-cstate.enabled-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-hardware_multithreading-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-pstate.scaling_governor-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-pstate.status-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-pstate.turbo-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-rdt.RDTCMT-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-rdt.RDTL3CA-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-rdt.RDTMBA-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-rdt.RDTMBM-
kubectl label node pmem-csi feature.node.kubernetes.io/cpu-rdt.RDTMON-
kubectl label node pmem-csi feature.node.kubernetes.io/kernel-config.NO_HZ-
kubectl label node pmem-csi feature.node.kubernetes.io/kernel-config.NO_HZ_IDLE-
kubectl label node pmem-csi feature.node.kubernetes.io/kernel-version.full-
kubectl label node pmem-csi feature.node.kubernetes.io/kernel-version.major-
kubectl label node pmem-csi feature.node.kubernetes.io/kernel-version.minor-
kubectl label node pmem-csi feature.node.kubernetes.io/kernel-version.revision-
kubectl label node pmem-csi feature.node.kubernetes.io/memory-numa-
kubectl label node pmem-csi feature.node.kubernetes.io/memory-nv.dax-
kubectl label node pmem-csi feature.node.kubernetes.io/memory-nv.present-
kubectl label node pmem-csi feature.node.kubernetes.io/network-sriov.capable-
kubectl label node pmem-csi feature.node.kubernetes.io/pci-0300_1a03.present-
kubectl label node pmem-csi feature.node.kubernetes.io/storage-nonrotationaldisk-
kubectl label node pmem-csi feature.node.kubernetes.io/system-os_release.ID-
kubectl label node pmem-csi feature.node.kubernetes.io/system-os_release.VERSION_ID-
kubectl label node pmem-csi feature.node.kubernetes.io/system-os_release.VERSION_ID.major-
kubectl label node pmem-csi feature.node.kubernetes.io/system-os_release.VERSION_ID.minor-
kubectl get no -o json | jq .items[].metadata.labels
}

generateyamls()
{
cat <<EOF > og-pmem-csi.yaml
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: og-pmem-csi
  namespace: pmem-csi
spec:
  targetNamespaces:
  - pmem-csi
EOF

cat <<EOF > subscription-pmem-csi.yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: pmem-csi
  namespace: pmem-csi
spec:
  channel: stable
  name: pmem-csi-operator
  source: operatorhubio-catalog
  sourceNamespace: olm
  installPlanApproval: Automatic
EOF

cat <<EOF > pmemcsideployment.yaml
apiVersion: pmem-csi.intel.com/v1beta1
kind: PmemCSIDeployment
metadata:
  name: pmem-csi.intel.com
spec:
  deviceMode: lvm
  nodeSelector:
    feature.node.kubernetes.io/memory-nv.dax: "true"
EOF
}

if [ "$1" == 'play' ] ; then
  if [ -n "$2" ] ; then
    screen$2
  else
    for n in $(seq 8) ; do screen$n ; sleep 3; done
  fi
elif [ "$1" == 'cleanup' ] ; then
  cleanupeverything
elif [ "$1" == 'generateyamls' ] ; then
  generateyamls
elif [ "$1" == 'record' ] ; then
  record
else
   echo 'Usage: screencast-pmem.sh [--help|help|-h] | [play [<screen number>]] | [cleanup] | [generateyamls] | [record]'
fi
