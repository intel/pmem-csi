# Installation and Usage

- [Prerequisites](#prerequisites)
    - [Software required](#software-required)
    - [Hardware required](#hardware-required)
    - [Persistent memory pre-provisioning](#persistent-memory-pre-provisioning)
- [Installation and setup](#installation-and-setup)
    - [Install PMEM-CSI driver](#install-pmem-csi-driver)
      - [Install using the operator](#install-using-the-operator)
      - [Install from source](#install-from-source)
    - [Expose persistent and cache volumes to applications](#expose-persistent-and-cache-volumes-to-applications)
    - [Kata Containers support](#kata-containers-support)
    - [Ephemeral inline volumes](#ephemeral-inline-volumes)
    - [Raw block volumes](#raw-block-volumes)
    - [Enable scheduler extensions](#enable-scheduler-extensions)
    - [Metrics support](#metrics-support)
- [PMEM-CSI Deployment CRD](#pmem-csi-deployment-crd)
- [Filing issues and contributing](#filing-issues-and-contributing)

## Overview

This section summarizes the steps that may be needed during the entire
lifecycle of PMEM in a cluster, starting with the initial preparations and
ending with decommissioning the hardware. The other sections explain
each step in more detail.

When setting up a cluster, the administrator must install the PMEM
hardware on nodes and configure some or all of those instances of PMEM for usage by
PMEM-CSI ([prerequisites](#prerequisites)]. Nodes where PMEM-CSI is
supposed to run must have a certain [label in
Kubernetes](#installation-and-setup).

The administrator must install PMEM-CSI, using [the PMEM-CSI
operator](#install-using-the-operator) (recommended) or with [scripts
and YAML files in the source code](#install-from-source). The default
install settings should work for most clusters. Some clusters don't
use `/var/lib/kubelet` as the data directory for kubelet and then the
corresponding PMEM-CSI setting must be changed accordingly because
otherwise kubelet does not find PMEM-CSI. The operator has an option
for that in its API (`kubeletDir` in the [`DeploymentSpec`](#deploymentspec)),
the YAML files can be edited or modified with
kustomize.

A PMEM-CSI installation can only use [direct device
mode](/docs/design.md#direct-device-mode) or [LVM
device mode](/docs/design.md#direct-device-mode). It is possible to install
PMEM-CSI twice on the same cluster with different modes, with these restrictions:

- The driver names must be different.
- The installations must run on different nodes by using different node
  labels, or the "usage" parameter of the LVM mode driver installation
  one a node must be so that it leaves spaces available for the direct mode
  driver installation on that same node.

The administrator must decide which storage classes shall be available
to users of the cluster. A storage class references a driver
installation by name, which indirectly determines the device mode. A
storage class also chooses which filesystem is used (xfs or ext4) and
enables [Kata Containers support](#kata-containers-support).

Optionally, the administrator can enable [the scheduler
extensions](#enable-scheduler-extensions) (recommended) and monitoring
of resource usage via the [metrics support](#metrics-support).

When using the operator, existing PMEM-CSI installations can be
upgraded seamlessly by installing a newer version of the
operator. Downgrading by installing an older version is also
supported, but may need manual work which will be documented in the
release notes.

When using YAML files, the only reliable way of up- or downgrading is
to remove the installation and install anew.

Users can then create PMEM volumes via [volume
claims](#expose-persistent-and-cache-volumes-to-applications) that
reference the storage classes or via [ephemeral inline
volumes](#ephemeral-inline-volumes).

A node should only be removed from a cluster after ensuring that there
is no pod running on it which uses PMEM and that there is no
persistent volume (`PV`) on it. This can be checked via `kubectl get
-o yaml pv` and looking for a `nodeAffinity` entry that references the
node or via metrics data for the node. When removing a node or even
the entire PMEM-CSI driver installation too soon, attempts to remove
pods or volumes via the Kubernetes API will fail. Administrators can
recover from that by force-deleting PVs for which the underlying
hardware has already been removed.

By default, PMEM-CSI wipes volumes after usage
([`eraseAfter`](#kubernetes-csi-specific)), so shredding PMEM hardware
after decomissioning it is optional.

## Prerequisites

### Software required

The recommended mimimum Linux kernel version for running the PMEM-CSI driver is 4.15. See [Persistent Memory Programming](https://pmem.io/2018/05/15/using_persistent_memory_devices_with_the_linux_device_mapper.html) for more details about supported kernel versions.

### Hardware required

Persistent memory device(s) are required for operation. However, some
development and testing can be done using QEMU-emulated persistent
memory devices. See the ["QEMU and Kubernetes"](autotest.md#qemu-and-kubernetes)
section for the commands that create such a virtual test cluster.

### Persistent memory pre-provisioning

The PMEM-CSI driver needs pre-provisioned regions on the NVDIMM
device(s). The PMEM-CSI driver itself intentionally leaves that to the
administrator who then can decide how much and how PMEM is to be used
for PMEM-CSI.

Beware that the PMEM-CSI driver will run without errors on a node
where PMEM was not prepared for it. It will then report zero local
storage for that node, something that currently is only visible in the
log files.

When running the Kubernetes cluster and PMEM-CSI on bare metal,
the [ipmctl](https://github.com/intel/ipmctl) utility can be used to create regions.
App Direct Mode has two configuration options - interleaved or non-interleaved.
One region per each NVDIMM is created in non-interleaved configuration.
In such a configuration, a PMEM-CSI volume cannot be larger than one NVDIMM.

Example of creating regions without interleaving, using all NVDIMMs:
``` console
$ ipmctl create -goal PersistentMemoryType=AppDirectNotInterleaved
```

Alternatively, multiple NVDIMMs can be combined to form an interleaved set.
This causes the data to be striped over multiple NVDIMM devices
for improved read/write performance and allowing one region (also, PMEM-CSI volume)
to be larger than single NVDIMM.

Example of creating regions in interleaved mode, using all NVDIMMs:
``` console
$ ipmctl create -goal PersistentMemoryType=AppDirect
```

When running inside virtual machines, each virtual machine typically
already gets access to one region and `ipmctl` is not needed inside
the virtual machine. Instead, that region must be made available for
use with PMEM-CSI because when the virtual machine comes up for the
first time, the entire region is already allocated for use as a single
block device:
``` console
$ ndctl list -RN
{
  "regions":[
    {
      "dev":"region0",
      "size":34357641216,
      "available_size":0,
      "max_available_extent":0,
      "type":"pmem",
      "persistence_domain":"unknown",
      "namespaces":[
        {
          "dev":"namespace0.0",
          "mode":"raw",
          "size":34357641216,
          "sector_size":512,
          "blockdev":"pmem0"
        }
      ]
    }
  ]
}
$ ls -l /dev/pmem*
brw-rw---- 1 root disk 259, 0 Jun  4 16:41 /dev/pmem0
```

Labels must be initialized in such a region, which must be performed
once after the first boot:
``` console
$ ndctl disable-region region0
disabled 1 region
$ ndctl init-labels nmem0
initialized 1 nmem
$ ndctl enable-region region0
enabled 1 region
$ ndctl list -RN
[
  {
    "dev":"region0",
    "size":34357641216,
    "available_size":34357641216,
    "max_available_extent":34357641216,
    "type":"pmem",
    "iset_id":10248187106440278,
    "persistence_domain":"unknown"
  }
]
$ ls -l /dev/pmem*
ls: cannot access '/dev/pmem*': No such file or directory
```

## Installation and setup

This section assumes that a Kubernetes cluster is already available
with at least one node that has persistent memory device(s). For development or
testing, it is also possible to use a cluster that runs on QEMU virtual
machines, see the ["QEMU and Kubernetes"](autotest.md#qemu-and-kubernetes).

- **Make sure that the alpha feature gates CSINodeInfo and CSIDriverRegistry are enabled**

The method to configure alpha feature gates may vary, depending on the Kubernetes deployment.
It may not be necessary anymore when the feature has reached beta state, which depends
on the Kubernetes version.

- **Label the cluster nodes that provide persistent memory device(s)**

``` console
$ kubectl label node <your node> storage=pmem
```

### Install PMEM-CSI driver

PMEM-CSI driver can be deployed to a Kubernetes cluster either using the
PMEM-CSI operator or by using reference yaml files provided in source code.

#### Install using the operator

[PMEM-CSI operator](./design.md#operator) facilitates deploying and managing
the [PMEM-CSI driver](https://github.com/intel/pmem-csi) on a Kubernetes cluster.

- **Setup deployment certificates**

By default, the operator creates the needed private keys and certificates required
for running the driver as described in [driver security](./design.md#security)
section. Those certificates are generated by the operator using a self-signed CA.
This can be overridden with custom keys and certificates by using appropriate fields
in the [deployment specification](#deploymentspec).These encoded certificates and
private keys are made available to driver pods via Kubernetes [secrets](https://kubernetes.io/docs/concepts/configuration/secret/)
by the operator.

**NOTE:** A production deployment that is not supposed to depend on the 
operator's self-signed CA instead must provide the certificates generated
from a trusted certificate authority.

- **Install the PMEM-CSI operator**

``` console
$ kubectl create -f https://github.com/intel/pmem-csi/raw/v0.8.0/deploy/operator/pmem-csi-operator.yaml
```

- **Create a custom resource**

Once the operator deployed in the previous step is in the `Running` state,
it is ready to handle PMEM-CSI `Deployment` objects in the `pmem-csi.intel.com`
API group. Refer to the [Deployment CRD API](#PMEM-CSI-Deployment-CRD) for
a complete list of supported properties.

Here is a minimal example driver deployment created with a custom resource:

``` console
$ kubectl create -f - <<EOF
apiVersion: pmem-csi.intel.com/v1alpha1
kind: Deployment
metadata:
  name: pmem-csi.intel.com
spec:
  deviceMode: lvm
  nodeSelector:
    storage: pmem
EOF
```

This uses the same `pmem-csi.intel.com` driver name as the YAML files
in [`deploy`](/deploy) and the node label from the [hardware
installation and setup section](#installation-and-setup).

Once the above deployment installation is successful, we can see all the driver
pods in `Running` state:
``` console
$ kubectl get deployments.pmem-csi.intel.com
NAME                 AGE
pmem-csi.intel.com   50s

$ kubectl describe deployment.pmem-csi.intel.com/pmem-csi.intel.com
Name:         pmem-csi.intel.com
Namespace:    default
Labels:       <none>
Annotations:  <none>
API Version:  pmem-csi.intel.com/v1alpha1
Kind:         Deployment
Metadata:
  Creation Timestamp:  2020-01-23T13:40:32Z
  Generation:          1
  Resource Version:    3596387
  Self Link:           /apis/pmem-csi.intel.com/v1alpha1/deployments/pmem-csi.intel.com
  UID:                 454b5961-5aa2-41c3-b774-29fe932ae236
Spec:
  Controller Resources:
    Requests:
      Cpu:      200m
      Memory:   100Mi
  Device Mode:  lvm
  Image:        localhost/pmem-csi-driver:canary
  Node Resources:
    Requests:
      Cpu:     200m
      Memory:  100Mi
Status:
  Phase:  Running
Events:
  Type    Reason         Age                From               Message
  ----    ------         ----               ----               -------
  Normal  NewDeployment  34s                pmem-csi-operator  Processing new driver deployment
  Normal  Running        2s (x10 over 26s)  pmem-csi-operator  Driver deployment successful


$ kubectl get po
NAME                               READY   STATUS    RESTARTS   AGE
pmem-csi-intel-com-controller-0    2/2     Running   0          51s
pmem-csi-intel-com-node-4x7cv      2/2     Running   0          50s
pmem-csi-intel-com-node-6grt6      2/2     Running   0          50s
pmem-csi-intel-com-node-msgds      2/2     Running   0          51s
pmem-csi-operator-749c7c7c69-k5k8n 1/1     Running   0          3m
```

#### Install from source

- **Get source code**

PMEM-CSI uses Go modules and thus can be checked out and (if that should be desired)
built anywhere in the filesystem. Pre-built container images are available and thus
users don't need to build from source, but they may need some additional files
for the following sections.
To get the source code, use:

``` console
$ git clone https://github.com/intel/pmem-csi
```

- **Set up certificates**

Certificates are required as explained in [Security](design.md#security).
If you are not using the test cluster described in
[Starting and stopping a test cluster](autotest.md#starting-and-stopping-a-test-cluster)
where certificates are created automatically, you must set up certificates manually.
This can be done by running the `./test/setup-ca-kubernetes.sh` script for your cluster.
This script requires "cfssl" tools which can be downloaded.
These are the steps for manual set-up of certificates:

- Download cfssl tools

``` console
$ curl -L https://pkg.cfssl.org/R1.2/cfssl_linux-amd64 -o _work/bin/cfssl --create-dirs
$ curl -L https://pkg.cfssl.org/R1.2/cfssljson_linux-amd64 -o _work/bin/cfssljson --create-dirs
$ chmod a+x _work/bin/cfssl _work/bin/cfssljson
```

- Run certificates set-up script

``` console
$ KUBCONFIG="<<your cluster kubeconfig path>>" PATH="$PWD/_work/bin:$PATH" ./test/setup-ca-kubernetes.sh
```

- **Deploy the driver to Kubernetes**

The `deploy/kubernetes-<kubernetes version>` directory contains
`pmem-csi*.yaml` files which can be used to deploy the driver on that
Kubernetes version. The files in the directory with the highest
Kubernetes version might also work for more recent Kubernetes
releases. All of these deployments use images published by Intel on
[Docker Hub](https://hub.docker.com/u/intel).

For each Kubernetes version, four different deployment variants are provided:

   - `direct` or `lvm`: one uses direct device mode, the other LVM device mode.
   - `testing`: the variants with `testing` in the name enable debugging
     features and shouldn't be used in production.

For example, to deploy for production with LVM device mode onto Kubernetes 1.17, use:

``` console
$ kubectl create -f deploy/kubernetes-1.17/pmem-csi-lvm.yaml
```

The PMEM-CSI [scheduler extender](design.md#scheduler-extender) and
[webhook](design.md#pod-admission-webhook) are not enabled in this basic
installation. See [below](#enable-scheduler-extensions) for
instructions about that.

These variants were generated with
[`kustomize`](https://github.com/kubernetes-sigs/kustomize).
`kubectl` >= 1.14 includes some support for that. The sub-directories
of [deploy/kustomize](/deploy/kustomize)`-<kubernetes version>` can be used as bases
for `kubectl kustomize`. For example:

   - Change namespace:
     ``` ShellSession
     $ mkdir -p my-pmem-csi-deployment
     $ cat >my-pmem-csi-deployment/kustomization.yaml <<EOF
     namespace: pmem-csi
     bases:
       - ../deploy/kubernetes-1.17/lvm
     EOF
     $ kubectl create namespace pmem-csi
     $ kubectl create --kustomize my-pmem-csi-deployment
     ```

   - Configure how much PMEM is used by PMEM-CSI for LVM
     (see [Namespace modes in LVM device mode](design.md#namespace-modes-in-lvm-device-mode)):
     ``` ShellSession
     $ mkdir -p my-pmem-csi-deployment
     $ cat >my-pmem-csi-deployment/kustomization.yaml <<EOF
     bases:
       - ../deploy/kubernetes-1.17/lvm
     patchesJson6902:
       - target:
           group: apps
           version: v1
           kind: DaemonSet
           name: pmem-csi-node
         path: lvm-parameters-patch.yaml
     EOF
     $ cat >my-pmem-csi-deployment/lvm-parameters-patch.yaml <<EOF
     # pmem-driver is in the container #0. Append arguments at the end.
     - op: add
       path: /spec/template/spec/containers/0/args/-
       value: "-pmemPercentage=90"
     EOF
     $ kubectl create --kustomize my-pmem-csi-deployment
     ```

- Wait until all pods reach 'Running' status

``` console
$ kubectl get pods
NAME                    READY   STATUS    RESTARTS   AGE
pmem-csi-node-8kmxf     2/2     Running   0          3m15s
pmem-csi-node-bvx7m     2/2     Running   0          3m15s
pmem-csi-controller-0   2/2     Running   0          3m15s
pmem-csi-node-fbmpg     2/2     Running   0          3m15s
```

Once after the driver deployed using one of the methods mentioned above
verify that the node labels have been configured correctly

``` console
$ kubectl get nodes --show-labels
```

The command output must indicate that every node with PMEM has these two labels:
``` console
pmem-csi.intel.com/node=<NODE-NAME>,storage=pmem
```

If **storage=pmem** is missing, label manually as described above. If
**pmem-csi.intel.com/node** is missing, then double-check that the
alpha feature gates are enabled, that the CSI driver is running on the node,
and that the driver's log output doesn't contain errors.

- **Define two storage classes using the driver**

``` console
$ kubectl create -f deploy/common/pmem-storageclass-ext4.yaml
$ kubectl create -f deploy/common/pmem-storageclass-xfs.yaml
```

- **Provision two PMEM-CSI volumes**

``` console
$ kubectl create -f deploy/common/pmem-pvc.yaml
```

- **Verify two Persistent Volume Claims have 'Bound' status**

``` console
$ kubectl get pvc
NAME                STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS       AGE
pmem-csi-pvc-ext4   Bound    pvc-f70f7b36-6b36-11e9-bf09-deadbeef0100   4Gi        RWO            pmem-csi-sc-ext4   16s
pmem-csi-pvc-xfs    Bound    pvc-f7101fd2-6b36-11e9-bf09-deadbeef0100   4Gi        RWO            pmem-csi-sc-xfs    16s
```

- **Start two applications requesting one provisioned volume each**

``` console
$ kubectl create -f deploy/common/pmem-app.yaml
```

These applications use **storage: pmem** in the <i>nodeSelector</i>
list to ensure scheduling to a node supporting pmem device, and each requests a mount of a volume,
one with ext4-format and another with xfs-format file system.

- **Verify two application pods reach 'Running' status**

``` console
$ kubectl get po my-csi-app-1 my-csi-app-2
NAME           READY   STATUS    RESTARTS   AGE
my-csi-app-1   1/1     Running   0          6m5s
NAME           READY   STATUS    RESTARTS   AGE
my-csi-app-2   1/1     Running   0          6m1s
```

- **Check that applications have a pmem volume mounted with added dax option**

``` console
$ kubectl exec my-csi-app-1 -- df /data
Filesystem           1K-blocks      Used Available Use% Mounted on
/dev/ndbus0region0fsdax/5ccaa889-551d-11e9-a584-928299ac4b17
                       4062912     16376   3820440   0% /data

$ kubectl exec my-csi-app-2 -- df /data
Filesystem           1K-blocks      Used Available Use% Mounted on
/dev/ndbus0region0fsdax/5cc9b19e-551d-11e9-a584-928299ac4b17
                       4184064     37264   4146800   1% /data

$ kubectl exec my-csi-app-1 -- mount |grep /data
/dev/ndbus0region0fsdax/5ccaa889-551d-11e9-a584-928299ac4b17 on /data type ext4 (rw,relatime,dax)

$ kubectl exec my-csi-app-2 -- mount |grep /data
/dev/ndbus0region0fsdax/5cc9b19e-551d-11e9-a584-928299ac4b17 on /data type xfs (rw,relatime,attr2,dax,inode64,noquota)
```

#### Expose persistent and cache volumes to applications

Kubernetes cluster administrators can expose persistent and cache volumes
to applications using
[`StorageClass
Parameters`](https://kubernetes.io/docs/concepts/storage/storage-classes/#parameters). An
optional `persistencyModel` parameter differentiates how the
provisioned volume can be used:

* no `persistencyModel` parameter or `persistencyModel: normal` in `StorageClass`  

  A normal Kubernetes persistent volume. In this case
  PMEM-CSI creates PMEM volume on a node and the application that
  claims to use this volume is supposed to be scheduled onto this node
  by Kubernetes. Choosing of node is depend on StorageClass
  `volumeBindingMode`. In case of `volumeBindingMode: Immediate`
  PMEM-CSI chooses a node randomly, and in case of `volumeBindingMode:
  WaitForFirstConsumer` (also known as late binding) Kubernetes first chooses a node for scheduling
  the application, and PMEM-CSI creates the volume on that
  node. Applications which claim a normal persistent volume has to use
  `ReadOnlyOnce` access mode in its `accessModes` list. This
  [diagram](/docs/images/sequence/pmem-csi-persistent-sequence-diagram.png)
  illustrates how a normal persistent volume gets provisioned in
  Kubernetes using PMEM-CSI driver.

* `persistencyModel: cache`  

  Volumes of this type shall be used in combination with
  `volumeBindingMode: Immediate`. In this case, PMEM-CSI creates a set
  of PMEM volumes each volume on different node. The number of PMEM
  volumes to create can be specified by `cacheSize` StorageClass
  parameter. Applications which claim a `cache` volume can use
  `ReadWriteMany` in its `accessModes` list. Try it out with the provided
  [cache storage class](/deploy/common/pmem-storageclass-cache.yaml)
  example. This
  [diagram](/docs/images/sequence/pmem-csi-cache-sequence-diagram.png)
  illustrates how a cache volume gets provisioned in Kubernetes using
  PMEM-CSI driver.

**NOTE**: Cache volumes are associated with a node, not a pod. Multiple
pods using the same cache volume on the same node will not get their
own instance but will end up sharing the same PMEM volume instead.
Application deployment has to consider this and use available Kubernetes
mechanisms like [node
anti-affinity](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#affinity-and-anti-affinity).
Try it out with provided
[cache application](/deploy/common/pmem-app-cache.yaml) example.

**WARNING**: late binding (`volumeBindingMode:WaitForFirstConsume`) has some caveats:
* Pod creation may get stuck when there isn't enough capacity left for
  the volumes; see the next section for details.
* A node is only chosen the first time a pod starts. After that it will always restart
  on that node, because that is where the persistent volume was created.

### Kata Containers support

[Kata Containers support](design.md#kata-containers-support) gets enabled via
the `kataContainers` storage class parameter. It accepts the following
values:
* `true/1/t/TRUE`  
  Create the filesystem inside a partition inside a file, try to mount
  on the host through a loop device with `-o dax` but proceed without
  `-o dax` when the kernel does not support that. Currently Linux up
  to and including 5.4 do not support it. In other words, on the host
  such volumes are usable, but only without DAX. Inside Kata
  Containers, DAX works.
* `false/0/f/FALSE` (default)  
  Create the filesystem directly on the volume.

[Raw block volumes](#raw-block-volumes) are only supported with
`kataContainers: false`. Attempts to create them with `kataContainers:
true` are rejected.

At the moment (= Kata Containers 1.11.0-rc0), only Kata Containers
with QEMU enable the special support for such volumes. Without QEMU or
with older releases of Kata Containers, the volume is still usable
through the normal remote filesystem support (9p or virtio-fs). Support
for Cloud Hypervisor is [in
progress](https://github.com/kata-containers/runtime/issues/2575).

With Kata Containers for QEMU, the VM must be configured appropriately
to allow adding the PMEM volumes to their address space. This can be
done globally by setting the `memory_offset` in the
[`configuration-qemu.toml`
file](https://github.com/kata-containers/runtime/blob/ee985a608015d81772901c1d9999190495fc9a0a/cli/config/configuration-qemu.toml.in#L86-L91)
or per-pod by setting the
[`io.katacontainers.config.hypervisor.memory_offset`
label](https://github.com/kata-containers/documentation/blob/master/how-to/how-to-set-sandbox-config-kata.md#hypervisor-options)
in the pod meta data. In both cases, the value has to be large enough
for all PMEM volumes used by the pod, otherwise pod creation will fail
with an error similar to this:

``` console
Error: container create failed: QMP command failed: not enough space, currently 0x8000000 in use of total space for memory devices 0x3c100000
```

The examples for usage of Kata Containers [with
ephemeral](/deploy/common/pmem-kata-app-ephemeral.yaml) and
[persistent](/deploy/common/pmem-kata-app.yaml) volumes use the pod
label. They assume that the `kata-qemu` runtime class [is
installed](https://github.com/kata-containers/packaging/tree/1.11.0-rc0/kata-deploy#run-a-sample-workload).

For the QEMU test cluster,
[`setup-kata-containers.sh`](/test/setup-kata-containers.sh) can be
used to install Kata Containers. However, this currently only works on
Clear Linux because on Fedora, the Docker container runtime is used
and Kata Containers does not support that one.


### Ephemeral inline volumes

#### Kubernetes CSI specific

This is the original implementation of ephemeral inline volumes for
CSI drivers in Kubernetes. It is currently available as a beta feature
in Kubernetes.

Volume requests [embedded in the pod spec](https://kubernetes-csi.github.io/docs/ephemeral-local-volumes.html#example-of-inline-csi-pod-spec) are provisioned as
ephemeral volumes. The volume request could use below fields as
[`volumeAttributes`](https://kubernetes.io/docs/concepts/storage/volumes/#csi):

|key|meaning|optional|values|
|---|-------|--------|-------------|
|`size`|Size of the requested ephemeral volume as [Kubernetes memory string](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#meaning-of-memory) ("1Mi" = 1024*1024 bytes, "1e3K = 1000000 bytes)|No||
|`eraseAfter`|Clear all data after use and before<br> deleting the volume|Yes|`true` (default),<br> `false`|
|`kataContainers`|Prepare volume for use in Kata Containers.|Yes|`false/0/f/FALSE` (default),<br> `true/1/t/TRUE`|

Try out ephemeral volume usage with the provided [example
application](/deploy/common/pmem-app-ephemeral.yaml).


#### Generic

This approach was introduced in [Kubernetes
1.19](https://kubernetes.io/blog/2020/09/01/ephemeral-volumes-with-storage-capacity-tracking/)
with the goal of using them for PMEM-CSI instead of the older
approach. In contrast CSI ephemeral inline volumes, no changes are
needed in CSI drivers, so PMEM-CSI already fully supports this if the
cluster has the feature enabled. See
[`pmem-app-generic-ephemeral.yaml`](/deploy/common/pmem-app-generic-ephemeral.yaml)
for an example.

When using generic ephemeral inline volumes together with [storage
capacity tracking](#storage-capacity-tracking), the [PMEM-CSI
scheduler extensions](#enable-scheduler-extensions) are not needed
anymore.

### Raw block volumes

Applications can use volumes provisioned by PMEM-CSI as [raw block
devices](https://kubernetes.io/blog/2019/03/07/raw-block-volume-support-to-beta/). Such
volumes use the same "fsdax" namespace mode as filesystem volumes
and therefore are block devices. That mode only supports dax (=
`mmap(MAP_SYNC)`) through a filesystem. Pages mapped on the raw block
device go through the Linux page cache. Applications have to format
and mount the raw block volume themselves if they want dax. The
advantage then is that they have full control over that part.

For provisioning a PMEM volume as raw block device, one has to create a 
`PersistentVolumeClaim` with `volumeMode: Block`. See example [PVC](
/deploy/common/pmem-pvc-block-volume.yaml) and
[application](/deploy/common/pmem-app-block-volume.yaml) for usage reference.

That example demonstrates how to handle some details:
- `mkfs.ext4` needs `-b 4096` to produce volumes that support dax;
  without it, the automatic block size detection may end up choosing
  an unsuitable value depending on the volume size.
- [Kubernetes bug #85624](https://github.com/kubernetes/kubernetes/issues/85624)
  must be worked around to format and mount the raw block device.

### Enable scheduler extensions

The PMEM-CSI scheduler extender and admission webhook are provided by
the PMEM-CSI controller. They need to be enabled during deployment via
the `--schedulerListen=[<listen address>]:<port>` parameter. The
listen address is optional and can be left out. The port is where a
HTTPS server will run. It uses the same certificates as the internal
gRPC service. When using the CA creation script described above, they
will contain alternative names for the URLs described in this section
(service names, `127.0.0.1` IP address).

This parameter can be added to one of the existing deployment files
with `kustomize`. All of the following examples assume that the
current directory contains the `deploy` directory from the PMEM-CSI
repository. It is also possible to reference the base via a
[URL](https://github.com/kubernetes-sigs/kustomize/blob/master/examples/remoteBuild.md).

``` ShellSession
$ mkdir my-pmem-csi-deployment

$ cat >my-pmem-csi-deployment/kustomization.yaml <<EOF
bases:
  - ../deploy/kubernetes-1.16/lvm
patchesJson6902:
  - target:
      group: apps
      version: v1
      kind: StatefulSet
      name: pmem-csi-controller
    path: scheduler-patch.yaml
EOF

$ cat >my-pmem-csi-deployment/scheduler-patch.yaml <<EOF
- op: add
  path: /spec/template/spec/containers/0/command/-
  value: "--schedulerListen=:8000"
EOF

$ kubectl create --kustomize my-pmem-csi-deployment
```

To enable the PMEM-CSI scheduler extender, a configuration file and an
additional `--config` parameter for `kube-scheduler` must be added to
the cluster control plane, or, if there is already such a
configuration file, one new entry must be added to the `extenders`
array. A full example is presented below.

The `kube-scheduler` must be able to connect to the PMEM-CSI
controller via the `urlPrefix` in its configuration. In some clusters
it is possible to use cluster DNS and thus a symbolic service name. If
that is the case, then deploy the [scheduler
service](/deploy/kustomize/scheduler/scheduler-service.yaml) as-is
and use `https://pmem-csi-scheduler.default.svc` as `urlPrefix`. If
the PMEM-CSI driver is deployed in a namespace, replace `default` with
the name of that namespace.

In a cluster created with kubeadm, `kube-scheduler` is unable to use
cluster DNS because the pod it runs in is configured with
`hostNetwork: true` and without `dnsPolicy`. Therefore the cluster DNS
servers are ignored. There also is no special dialer as in other
clusters. As a workaround, the PMEM-CSI service can be exposed via a
fixed node port like 32000 on all nodes. Then
`https://127.0.0.1:32000` needs to be used as `urlPrefix`. Here's how
the service can be created with that node port:

``` ShellSession
$ mkdir my-scheduler

$ cat >my-scheduler/kustomization.yaml <<EOF
bases:
  - ../deploy/kustomize/scheduler
patchesJson6902:
  - target:
      version: v1
      kind: Service
      name: pmem-csi-scheduler
    path: node-port-patch.yaml
EOF

$ cat >my-scheduler/node-port-patch.yaml <<EOF
- op: add
  path: /spec/ports/0/nodePort
  value: 32000
EOF

$ kubectl create --kustomize my-scheduler
```

How to (re)configure `kube-scheduler` depends on the cluster. With
kubeadm it is possible to set all necessary options in advance before
creating the master node with `kubeadm init`. One additional
complication with kubeadm is that `kube-scheduler` by default doesn't
trust any root CA. The following kubeadm config file solves
this together with enabling the scheduler configuration by
bind-mounting the root certificate that was used to sign the certificate used
by the scheduler extender into the location where the Go
runtime will find it. It works for Kubernetes <= 1.18:

``` ShellSession
$ sudo mkdir -p /var/lib/scheduler/
$ sudo cp _work/pmem-ca/ca.pem /var/lib/scheduler/ca.crt

# https://github.com/kubernetes/kubernetes/blob/52d7614a8ca5b8aebc45333b6dc8fbf86a5e7ddf/staging/src/k8s.io/kube-scheduler/config/v1alpha1/types.go#L38-L107
$ sudo sh -c 'cat >/var/lib/scheduler/scheduler-policy.cfg' <<EOF
{
  "kind" : "Policy",
  "apiVersion" : "v1",
  "extenders" :
    [{
      "urlPrefix": "https://<service name or IP>:<port>",
      "filterVerb": "filter",
      "prioritizeVerb": "prioritize",
      "nodeCacheCapable": true,
      "weight": 1,
      "managedResources":
      [{
        "name": "pmem-csi.intel.com/scheduler",
        "ignoredByScheduler": true
      }]
    }]
}
EOF

# https://github.com/kubernetes/kubernetes/blob/52d7614a8ca5b8aebc45333b6dc8fbf86a5e7ddf/staging/src/k8s.io/kube-scheduler/config/v1alpha1/types.go#L38-L107
$ sudo sh -c 'cat >/var/lib/scheduler/scheduler-config.yaml' <<EOF
apiVersion: kubescheduler.config.k8s.io/v1alpha1
kind: KubeSchedulerConfiguration
schedulerName: default-scheduler
algorithmSource:
  policy:
    file:
      path: /var/lib/scheduler/scheduler-policy.cfg
clientConnection:
  # This is where kubeadm puts it.
  kubeconfig: /etc/kubernetes/scheduler.conf
EOF

$ cat >kubeadm.config <<EOF
apiVersion: kubeadm.k8s.io/v1beta1
kind: ClusterConfiguration
scheduler:
  extraVolumes:
    - name: config
      hostPath: /var/lib/scheduler
      mountPath: /var/lib/scheduler
      readOnly: true
    - name: cluster-root-ca
      hostPath: /var/lib/scheduler/ca.crt
      mountPath: /etc/ssl/certs/ca.crt
      readOnly: true
  extraArgs:
    config: /var/lib/scheduler/scheduler-config.yaml
EOF

$ kubeadm init --config=kubeadm.config
```

In Kubernetes 1.19, the configuration API of the scheduler
changed. The corresponding command for Kubernetes >= 1.19 are:

``` ShellSession
$ sudo mkdir -p /var/lib/scheduler/
$ sudo cp _work/pmem-ca/ca.pem /var/lib/scheduler/ca.crt

# https://github.com/kubernetes/kubernetes/blob/1afc53514032a44d091ae4a9f6e092171db9fe10/staging/src/k8s.io/kube-scheduler/config/v1beta1/types.go#L44-L96
$ sudo sh -c 'cat >/var/lib/scheduler/scheduler-config.yaml' <<EOF
apiVersion: kubescheduler.config.k8s.io/v1beta1
kind: KubeSchedulerConfiguration
clientConnection:
  # This is where kubeadm puts it.
  kubeconfig: /etc/kubernetes/scheduler.conf
extenders:
- urlPrefix: https://127.0.0.1:<service name or IP>:<port>
  filterVerb: filter
  prioritizeVerb: prioritize
  nodeCacheCapable: true
  weight: 1
  managedResources:
  - name: pmem-csi.intel.com/scheduler
    ignoredByScheduler: true
EOF

$ cat >kubeadm.config <<EOF
apiVersion: kubeadm.k8s.io/v1beta1
kind: ClusterConfiguration
scheduler:
  extraVolumes:
    - name: config
      hostPath: /var/lib/scheduler
      mountPath: /var/lib/scheduler
      readOnly: true
    - name: cluster-root-ca
      hostPath: /var/lib/scheduler/ca.crt
      mountPath: /etc/ssl/certs/ca.crt
      readOnly: true
  extraArgs:
    config: /var/lib/scheduler/scheduler-config.yaml
EOF

$ kubeadm init --config=kubeadm.config
```

It is possible to stop here without enabling the pod admission webhook.
To enable also that, continue as follows.

First of all, it is recommended to exclude all system pods from
passing through the web hook. This ensures that they can still be
created even when PMEM-CSI is down:

``` console
$ kubectl label ns kube-system pmem-csi.intel.com/webhook=ignore
```

This special label is configured in [the provided web hook
definition](/deploy/kustomize/webhook/webhook.yaml). On Kubernetes >=
1.15, it can also be used to let individual pods bypass the webhook by
adding that label. The CA gets configured explicitly, which is
supported for webhooks.

``` ShellSession
$ mkdir my-webhook

$ cat >my-webhook/kustomization.yaml <<EOF
bases:
  - ../deploy/kustomize/webhook
patchesJson6902:
  - target:
      group: admissionregistration.k8s.io
      version: v1beta1
      kind: MutatingWebhookConfiguration
      name: pmem-csi-hook
    path: webhook-patch.yaml
EOF

$ cat >my-webhook/webhook-patch.yaml <<EOF
- op: replace
  path: /webhooks/0/clientConfig/caBundle
  value: $(base64 -w 0 _work/pmem-ca/ca.pem)
EOF

$ kubectl create --kustomize my-webhook
```
### Storage capacity tracking

[Kubernetes
1.19](https://kubernetes.io/blog/2020/09/01/ephemeral-volumes-with-storage-capacity-tracking/)
introduces support for publishing and using storage capacity
information for pod scheduling. PMEM-CSI must be deployed
differently to use this feature:

- `external-provisioner` must be told to publish storage capacity
  information via command line arguments.
- A flag in the CSI driver information must be set for the Kubernetes
  scheduler, otherwise it ignores that information when considering
  pods with unbound volume.

Because the `CSIStorageCapacity` feature is still alpha in 1.19 and
driver deployment would fail on a cluster without support for it, none
of the normal deployment files nor the operator enable that. Instead,
special kustomize variants are provided in
`deploy/kubernetes-1.19-alpha*`.

They can be used for example in the QEMU test cluster with:

```console
$ TEST_KUBERNETES_VERSION=1.19 make start
...
$ TEST_KUBERNETES_FLAVOR=-alpha test/setup-deployment.sh
INFO: deploying from /nvme/gopath/src/github.com/intel/pmem-csi/deploy/kubernetes-1.19-alpha/lvm/testing
...
```

### Metrics support

Metrics support is controlled by command line options of the PMEM-CSI
driver binary and of the CSI sidecars. Annotations and named container
ports make it possible to discover these data scraping endpoints. The
[metrics kustomize
base](/deploy/kustomize/kubernetes-with-metrics/kustomization.yaml)
adds all of that to the pre-generated deployment files. The operator
also enables the metrics support.

Access to metrics data is not restricted (no TLS, no client
authorization) because the metrics data is not considered confidential
and access control would just make client configuration unnecessarily
complex.

#### Metrics data

PMEM-CSI exposes metrics data about the Go runtime, Prometheus, CSI
method calls, and PMEM-CSI:

Name | Type | Explanation
-----|------|------------
`build_info` | gauge | A metric with a constant '1' value labeled by version.
`csi_[sidecar\|plugin]_operations_seconds` | histogram | gRPC call duration and error code, for sidecar to driver (aka plugin) communication.
`go_*` | | [Go runtime information](https://github.com/prometheus/client_golang/blob/master/prometheus/go_collector.go)
`pmem_amount_available` | gauge | Remaining amount of PMEM on the host that can be used for new volumes.
`pmem_amount_managed` | gauge | Amount of PMEM on the host that is managed by PMEM-CSI.
`pmem_amount_max_volume_size` | gauge | The size of the largest PMEM volume that can be created.
`pmem_amount_total` | gauge | Total amount of PMEM on the host.
`pmem_csi_[controller\|node]_operations_seconds` | histogram | gRPC call duration and error code, for the PMEM-CSI internal controller to node communication. The `node` label identifies the node.
`pmem_nodes` | gauge | The number of PMEM-CSI nodes registered in the controller.
`process_*` | | [Process information](https://github.com/prometheus/client_golang/blob/master/prometheus/process_collector.go)
`promhttp_metric_handler_requests_in_flight` | gauge | Current number of scrapes being served.
`promhttp_metric_handler_requests_total` | counter | Total number of scrapes by HTTP status code.

This list is tentative and may still change as long as metrics support
is alpha. To see all available data, query a container. Different
containers provide different data. For example, the controller
provides:

``` ShellSession
$ kubectl port-forward pmem-csi-controller-0 10010
Forwarding from 127.0.0.1:10010 -> 10010
Forwarding from [::1]:10010 -> 10010
```

And in another shell:

``` ShellSession
$ curl --silent http://localhost:10010/metrics | grep '# '
# HELP build_info A metric with a constant '1' value labeled by version.
# TYPE build_info gauge
...
# HELP csi_plugin_operations_seconds [ALPHA] Container Storage Interface operation duration with gRPC error code status total
# TYPE csi_plugin_operations_seconds histogram
...
# HELP pmem_csi_controller_operations_seconds [ALPHA] Container Storage Interface operation duration with gRPC error code status total
# TYPE pmem_csi_controller_operations_seconds histogram
...
```


#### Prometheus example

An [extension of the scrape config](/deploy/prometheus.yaml) is
necessary for [Prometheus](https://prometheus.io/). When deploying
[Prometheus via Helm](https://hub.helm.sh/charts/stable/prometheus),
that file can be added to the default configuration with the `-f`
parameter. The following example works for the [QEMU-based
cluster](autotest.md#qemu-and-kubernetes) and Helm v3.1.2. In a real
production deployment, some kind of persistent storage should be
provided. The [URL](/deploy/prometheus.yaml) can be used instead of
the file name, too.


``` ShellSession
$ helm install prometheus stable/prometheus \
     --set alertmanager.persistentVolume.enabled=false,server.persistentVolume.enabled=false \
     -f deploy/prometheus.yaml
NAME: prometheus
LAST DEPLOYED: Tue Aug 18 18:04:27 2020
NAMESPACE: default
STATUS: deployed
REVISION: 1
TEST SUITE: None
NOTES:
The Prometheus server can be accessed via port 80 on the following DNS name from within your cluster:
prometheus-server.default.svc.cluster.local


Get the Prometheus server URL by running these commands in the same shell:
  export POD_NAME=$(kubectl get pods --namespace default -l "app=prometheus,component=server" -o jsonpath="{.items[0].metadata.name}")
  kubectl --namespace default port-forward $POD_NAME 9090
#################################################################################
######   WARNING: Persistence is disabled!!! You will lose your data when   #####
######            the Server pod is terminated.                             #####
#################################################################################

...
```

After running this `kubectl port-forward` command, it is possible to
access the [Prometheus web
interface](https://prometheus.io/docs/prometheus/latest/getting_started/#using-the-expression-browser)
and run some queries there. Here are some examples for the QEMU test
cluster with two volumes created on node `pmem-csi-pmem-govm-worker2`.

Available PMEM as percentage:
```
pmem_amount_available / pmem_amount_managed
```

| Result variable | Value | Tags|
| ----------------|-------|-----|
| none | 0.7332986065893997 | instance = 10.42.0.1:10010 |
|  | | job = pmem-csi-containers |
|  | | kubernetes_namespace = default |
|  | | kubernetes_pod_container_name = pmem-driver |
|  | | kubernetes_pod_name = pmem-csi-node-dfkrw |
|  | | kubernetes_pod_node_name = pmem-csi-pmem-govm-worker2 |
|  | | node = pmem-csi-pmem-govm-worker2 |
|  | 1 | instance = 10.36.0.1:10010 |
|  | | job = pmem-csi-containers |
|  | | kubernetes_namespace = default |
|  | | kubernetes_pod_container_name = pmem-driver |
|  | | kubernetes_pod_name = pmem-csi-node-z5vnp |
|  | | kubernetes_pod_node_name pmem-csi-pmem-govm-worker3 |
|  | | node = pmem-csi-pmem-govm-worker3 |
|  | 1 | instance = 10.44.0.1:10010 |
|  | | job = pmem-csi-containers |
|  | | kubernetes_namespace = default |
|  | | kubernetes_pod_container_name = pmem-driver |
|  | | kubernetes_pod_name = pmem-csi-node-zzmsd |
|  | | kubernetes_pod_node_name = pmem-csi-pmem-govm-worker1 |
|  | | node = pmem-csi-pmem-govm-worker1 |


Number of `CreateVolume` calls in nodes:
```
pmem_csi_node_operations_seconds_count{method_name="/csi.v1.Controller/CreateVolume"}
```

|Result variable | Value | Tags|
|----------------|-------|------|
| `pmem_csi_node_operations_seconds_count` | 2 | driver_name = pmem-csi.intel.com |
| | | grpc_status_code = OK |
| | | instance = 10.42.0.1:10010 |
| | | job = pmem-csi-containers |
| | | kubernetes_namespace = default |
| | | kubernetes_pod_container_name = pmem-driver |
| | | kubernetes_pod_name = pmem-csi-node-dfkrw |
| | | kubernetes_pod_node_name = pmem-csi-pmem-govm-worker2 |
| | | method_name = /csi.v1.Controller/CreateVolume |
| | | node = pmem-csi-pmem-govm-worker2 |

## PMEM-CSI Deployment CRD

`Deployment` is a cluster-scoped Kubernetes resource in the
`pmem-csi.intel.com` API group. It describes how a PMEM-CSI driver
instance is to be created.

The operator will create objects in the namespace in which the
operator itself runs if the object type is namespaced.

The name of the deployment object is also used as CSI driver
name. This ensures that the name is unique and immutable. However,
name clashes with other CSI drivers are still possible, so the name
should meet the [CSI
requirements](https://github.com/container-storage-interface/spec/blob/master/spec.md#getplugininfo):
- domain name notation format, including a unique top-level domain
- 63 characters or less, beginning and ending with an alphanumeric
  character ([a-z0-9A-Z]) with dashes (-), dots (.), and
  alphanumerics between.

The name is also used as prefix for the names of all objects created
for the deployment and for the local `/var/lib/<name>` state directory
on each node.

The current API for PMEM-CSI `Deployment` resources is:

### Deployment

|Field | Type | Description |
|---|---|---|
| apiVersion | string  | `pmem-csi.intel.com/v1alpha1`|
| kind | string | `Deployment`|
| metadata | [ObjectMeta](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#metadata) | Object metadata, name used for CSI driver and as prefix for sub-objects |
| spec | [DeploymentSpec](#deployment-spec) | Specification of the desired behavior of the deployment |

#### DeploymentSpec

|Field | Type | Description | Default Value |
|---|---|---|---|
| image | string | PMEM-CSI docker image name used for the deployment | the same image as the operator<sup>1</sup> |
| provisionerImage | string | [CSI provisioner](https://kubernetes-csi.github.io/docs/external-provisioner.html) docker image name | latest [external provisioner](https://kubernetes-csi.github.io/docs/external-provisioner.html) stable release image<sup>2</sup> |
| nodeRegistrarImage | string | [CSI node driver registrar](https://github.com/kubernetes-csi/node-driver-registrar) docker image name | latest [node driver registrar](https://kubernetes-csi.github.io/docs/node-driver-registrar.html) stable release image<sup>2</sup> |
| pullPolicy | string | Docker image pull policy. either one of `Always`, `Never`, `IfNotPresent` | `IfNotPresent` |
| logLevel | integer | PMEM-CSI driver logging level | 3 |
| deviceMode | string | Device management mode to use. Supports one of `lvm` or `direct` | `lvm`
| controllerResources | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.12/#resourcerequirements-v1-core) | Describes the compute resource requirements for controller pod |
| nodeResources | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.12/#resourcerequirements-v1-core) | Describes the compute resource requirements for the pods running on node(s) |
| registryCert | string | Encoded tls certificate signed by a certificate authority used for driver's controller registry server | generated by operator self-signed CA |
| nodeControllerCert | string | Encoded tls certificate signed by a certificate authority used for driver's node controllers | generated by operator self-signed CA |
| registryKey | string | Encoded RSA private key used for signing by `registryCert` | generated by the operator |
| nodeControllerKey | string | Encoded RSA private key used for signing by `nodeControllerCert` | generated by the operator |
| caCert | string | Certificate of the CA by which the `registryCert` and `controllerCert` are signed | self-signed certificate generated by the operator |
| nodeSelector | string map | [Labels to use for selecting Nodes](../docs/install.md#run-pmem-csi-on-kubernetes) on which PMEM-CSI driver should run. | `{ "storage": "pmem" }`|
| pmemPercentage | integer | Percentage of PMEM space to be used by the driver on each node. This is only valid for a driver deployed in `lvm` mode. This field can be modified, but by that time the old value may have been used already. Reducing the percentage is not supported. | 100 |
| labels | string map | Additional labels for all objects created by the operator. Can be modified after the initial creation, but removed labels will not be removed from existing objects because the operator cannot know which labels it needs to remove and which it has to leave in place. |
| kubeletDir | string | Kubelet's root directory path | /var/lib/kubelet |

<sup>1</sup> To use the same container image as default driver image
the operator pod must set with below environment variables with
appropriate values:
 - POD_NAME: Name of the operator pod. Namespace of the pod could be figured out by the operator.
 - OPERATOR_NAME: Name of the operator container. If not set, defaults to "pmem-csi-operator"

<sup>2</sup> Image versions depend on the Kubernetes release. The
operator dynamically chooses suitable image versions. Users have to
take care of that themselves when overriding the values.

**WARNING**: although all fields can be modified and changes will be
propagated to the deployed driver, not all changes are safe. In
particular, changing the `deviceMode` will not work when there are
active volumes.

#### DeploymentStatus

A PMEM-CSI Deployment's `status` field is a `DeploymentStatus` object, which has
a `phase` field. The phase of a Deployment is high-level summary of where the
Deployment is in it's lifecycle.

The possible `phase` values and their meaning are as below:

| Value | Meaning |
|---|---|
| empty string | A new deployment. |
| Initializing | All the direct sub-resources of the `Deployment` are created, but some indirect ones (like pods controlled by a daemon set) may still be missing. |
| Running | The operator has determined that the driver is usable<sup>1</sup>.  |
| Failed | For some reason the state of the `Deployment` failed and cannot be progressed<sup>2</sup>. |

<sup>1</sup> This check has not been implemented yet. Instead, the deployment goes straight to `Running` after creating sub-resources.
<sup>2</sup> Failure reason is supposed to be carried by one of additional `DeploymentStatus` field, but not implemented yet.

#### Deployment Events

The PMEM-CSI operator posts events on the progress of a `Deployment`. If the
deployment is in the `Failed` state, then one can look into the event(s) using
`kubectl describe` on that deployment for the detailed failure reason.


> **Note on multiple deployments**
>
> Though the operator allows running multiple PMEM-CSI driver deployments, one
> has to take extreme care of such deployments by ensuring that not more than
> one driver ends up running on the same node(s). Nodes on which a PMEM-CSI
> driver could run can be configured by using `nodeSelector` property of
> [`DeploymentSpec`](#deployment-crd-api).


## Filing issues and contributing

Report a bug by [filing a new issue](https://github.com/intel/pmem-csi/issues).

Before making your first contribution, be sure to read the [development documentation](DEVELOPMENT.md)
for guidance on code quality and branches.

Contribute by [opening a pull request](https://github.com/intel/pmem-csi/pulls).

Learn [about pull requests](https://help.github.com/articles/using-pull-requests/).

**Reporting a Potential Security Vulnerability:** If you have discovered potential security vulnerability in PMEM-CSI, please send an e-mail to secure@intel.com. For issues related to Intel Products, please visit [Intel Security Center](https://security-center.intel.com).

It is important to include the following details:

- The projects and versions affected
- Detailed description of the vulnerability
- Information on known exploits

Vulnerability information is extremely sensitive. Please encrypt all security vulnerability reports using our [PGP key](https://www.intel.com/content/www/us/en/security-center/pgp-public-key.html).

A member of the Intel Product Security Team will review your e-mail and contact you to collaborate on resolving the issue. For more information on how Intel works to resolve security issues, see: [vulnerability handling guidelines](https://www.intel.com/content/www/us/en/security-center/vulnerability-handling-guidelines.html).
