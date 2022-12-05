# Installation and Usage

## Overview

This section summarizes the steps that may be needed during the entire
lifecycle of PMEM in a cluster, starting with the initial preparations and
ending with decommissioning the hardware. The other sections explain
each step in more detail.

When setting up a cluster, the administrator must install the PMEM
hardware on nodes and configure some or all of those instances of PMEM for usage by
PMEM-CSI ([prerequisites](#prerequisites)). Nodes where PMEM-CSI is
supposed to run must have a certain [label in
Kubernetes](#installation-and-setup).

The administrator must install PMEM-CSI, using [the PMEM-CSI
operator](#install-using-the-operator) (recommended) or with [scripts
and YAML files in the source code](#install-via-yaml-files). The default
install settings should work for most clusters. Some clusters don't
use `/var/lib/kubelet` as the data directory for kubelet and then the
corresponding PMEM-CSI setting must be changed accordingly because
otherwise kubelet does not find PMEM-CSI. The operator has an option
for that in its API (`kubeletDir` in the [`DeploymentSpec`](#deploymentspec)),
the YAML files can be edited or modified with
kustomize.

A PMEM-CSI installation can only use [direct device
mode](design.md#direct-device-mode) or [LVM
device mode](design.md#lvm-device-mode). It is possible to install
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
extensions](#enable-scheduler-extensions) and monitoring of resource
usage via the [metrics support](#metrics-support).

It is [recommended](./design.md#dynamic-provisioning-of-local-volumes)
to enable the scheduler extensions and use
`volumeBindingMode: WaitForFirstConsumer` as in the
[`pmem-storageclass-late-binding.yaml`](/deploy/common/pmem-storageclass-late-binding.yaml)
example. This ensures that pods get scheduled onto nodes that have
sufficient RAM, CPU and PMEM. Without the scheduler extensions, it is
random whether the scheduler picks a node that has PMEM available and
immediate binding (the default volume binding mode) might work
better. However, then pods might not be able to run when the node
where volumes were created are overloaded.

Starting with Kubernetes 1.21, PMEM-CSI uses [storage capacity
tracking](https://kubernetes.io/docs/concepts/storage/storage-capacity/)
to handle Pod scheduling and the scheduler extensions are not needed
anymore. `WaitForFirstConsumer` still is the recommended volume
binding mode.

Optionally, the log output format can be changed from the default
"text" format (= the traditional glog format) to "json" (= output via
[zap](https://github.com/uber-go/zap/blob/master/README.md)) for easier
processing.

When using the operator, existing PMEM-CSI installations can be
upgraded seamlessly by installing a newer version of the
operator. Downgrading by installing an older version is also
supported, but may need manual work which will be documented in the
release notes.

When using YAML files, the only reliable way of up- or downgrading is
to remove the installation and install anew.

Users can then create PMEM volumes via [persistent volume
claims](https://kubernetes.io/docs/concepts/storage/persistent-volumes/) that
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

If the operating system on the nodes does not provide `ipmctl`, then
it can also be run inside a container, using the PMEM-CSI image. The same
invocation works with `podman` instead of `docker`.
``` console
$ sudo docker run --privileged --rm -u 0:0 docker.io/intel/pmem-csi-driver:canary ipmctl help
Intel(R) Optane(TM) Persistent Memory Command Line Interface

    Usage: ipmctl <verb>[<options>][<targets>][<properties>]
...
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

On some virtual machines, for example VMware® vSphere, the persistent
memory does not support setting labels and the `ndctl init-labels
nmem0` command above would fail. What can be done in that case is to
convert the existing namespace from "raw" to "fsdax" mode and
then run PMEM-CSI in LVM mode. Direct mode is not possible because
it depends on creating additional namespaces which in turn depends
on support for labels. The command for conversion is:
```console
$ ndctl create-namespace --force --reconfig=namespace0.0 --mode=fsdax --name=pmem-csi
{
  "dev":"namespace0.0",
  "mode":"fsdax",
  "map":"dev",
  "size":67643637760,
  "uuid":"9fa6976c-ab57-491b-a00c-e52d092a4fa8",
  "sector_size":512,
  "align":2097152,
  "blockdev":"pmem0",
  "name":"pmem-csi"
}
```

Note the `pmem-csi` name for the namespace: this is how PMEM-CSI in
LVM mode knows that it is allowed to use this namespace. When the VM
provides only "legacy PMEM", ndctl silently drops that name. In that
case, the volume group as to be created manually:
```console
$ vgcreate --force bus0region0fsdax /dev/pmem0
```

See [automatic node setup](#automatic-node-setup) below for
instructions on how to automate this conversion.

## Installation and setup

This section assumes that a Kubernetes cluster is already available
with at least one node that has persistent memory device(s). For
development or testing, it is also possible to use a cluster that runs
on QEMU virtual machines, see the ["QEMU and
Kubernetes"](autotest.md#qemu-and-kubernetes).

- **Make sure that the alpha feature gates CSINodeInfo and CSIDriverRegistry are enabled**

The method to configure alpha feature gates may vary, depending on the Kubernetes deployment.
It may not be necessary anymore when the feature has reached beta state, which depends
on the Kubernetes version.

- **Label the cluster nodes that provide persistent memory device(s)**

PMEM-CSI manages PMEM on those nodes that have a certain label. For
historic reasons, the default in the YAML files and the operator
settings is to use a label `storage` with the value `pmem`.

Such a label can be set for each node manually with:

``` console
$ kubectl label node <your node> storage=pmem
```

Alternatively, the [Node Feature
Discovery (NFD)](https://kubernetes-sigs.github.io/node-feature-discovery/stable/get-started/index.html)
add-on can be used to label nodes automatically. In that case, the
default PMEM-CSI node selector has to be changed to
`"feature.node.kubernetes.io/memory-nv.dax": "true"`. The operator has
the [`nodeSelector`
field](https://kubernetes-sigs.github.io/node-feature-discovery/stable/get-started/index.html)
for that. For the YAML files a kustomize patch can be used.

### Install PMEM-CSI driver

PMEM-CSI driver can be deployed to a Kubernetes cluster either using the
PMEM-CSI operator or by using reference yaml files provided in source code.

#### Install using the operator

The [PMEM-CSI operator](./design.md#pmem-csi-operator) facilitates deploying and managing
the PMEM-CSI driver on a Kubernetes cluster.

##### Installing the operator from Operator Hub

If your cluster supports managing the operators using the [Operator
Lifecycle Manager](https://olm.operatorframework.io/), then it is
recommended to install the PMEM-CSI operator from the
[OperatorHub](https://operatorhub.io/operator/pmem-csi-operator). Follow
the instructions shown by the "Install" button. When using this
approach, the operator itself always runs with default parameters, in
particular log output in "text" format.

##### Installing the operator on an OpenShift cluster using the RedHat catalog

**NOTE**: PMEM-CSI operator will go into sustaining mode starting Red Hat
OpenShift platform 4.11. The last validation cycle for Red Hat OpenShift CSI
Operator Certification and Badging is on 4.10. Intel is winding down future
development and contributions including, but not limited to, maintenance, bug
fixes, new releases, or updates, to this project.

The current release of the PMEM-CSI driver and operator will
continue to be maintained and supported until EOL of Intel Optane PMem. New releases,
new features and bug fixes will be restricted only to business-critical
cases. Reference [Intel’s Optane Business Unit Closure
announcement](https://d1io3yog0oux5.cloudfront.net/_7476eb634cf21033bf2ce4974e02203e/intel/db/887/8856/prepared_remarks/Intel-CEO-CFO-2Q22-earnings-statements-1.pdf).

If you run an OpenShift cluster, then it is recommended to install the PMEM-CSI operator by
following the instructions shown by "Deploy & use" on [RedhatCatalog](https://catalog.redhat.com/software/operators/detail/612cd31535f8020a3f8bd5f2). 
The recommended approach is "Installing from OperatorHub using the web console".

##### Installing the operator from YAML

Alternatively, the you can install the operator manually from YAML files.
First install the PmemCSIDeployment CRD:

``` console
$ kubectl create -f https://github.com/intel/pmem-csi/raw/devel/deploy/crd/pmem-csi.intel.com_pmemcsideployments.yaml
```

Then install the PMEM-CSI operator itself:

``` console
$ kubectl create -f https://github.com/intel/pmem-csi/raw/devel/deploy/operator/pmem-csi-operator.yaml
```
The operator gets deployed in a namespace called 'pmem-csi' which gets created by that YAML file.

**WARNING:** This YAML file cannot be used to stop just the operator while
keeping the PMEM-CSI deployments running. That's because something like
`kubectl delete -f pmem-csi-operator.yaml` will delete the `pmem-csi`
namespace which then also causes all PMEM-CSI deployments that might have
been created in that namespace to be deleted.

##### Create a driver deployment

Once the operator is installed and running, it is ready to handle
`PmemCSIDeployment` objects in the `pmem-csi.intel.com` API group.
Refer to the [PmemCSIDeployment CRD API](#pmem-csi-deployment-crd)
for a complete list of supported properties.

Here is a minimal example driver deployment created with a custom resource:

**NOTE**: `nodeSelector` must match the node label that was set in the
[installation and setup](#installation-and-setup) section. The PMEM-CSI
[scheduler extender](design.md#scheduler-extender) and
[webhook](design.md#pod-admission-webhook) are not enabled in this basic
installation. See [below](#enable-scheduler-extensions) for
instructions about that.

``` ShellSession
$ kubectl create -f - <<EOF
apiVersion: pmem-csi.intel.com/v1beta1
kind: PmemCSIDeployment
metadata:
  name: pmem-csi.intel.com
spec:
  deviceMode: lvm
  nodeSelector:
    feature.node.kubernetes.io/memory-nv.dax: "true"
EOF
```

This uses the same `pmem-csi.intel.com` driver name as the YAML files
in [`deploy`](/deploy) and the node label created by NFD (see the [hardware
installation and setup section](#installation-and-setup)).

Once the above deployment installation is successful, we can see all the driver
pods in `Running` state:
``` console
$ kubectl get pmemcsideployments
NAME                 DEVICEMODE   NODESELECTOR   IMAGE   STATUS   AGE
pmem-deployment      lvm                                 Running  50s

$ kubectl describe pmemcsideployment/pmem-csi.intel.com
Name:         pmem-csi.intel.com
Namespace:
Labels:       <none>
Annotations:  <none>
API Version:  pmem-csi.intel.com/v1beta1
Kind:         PmemCSIDeployment
Metadata:
  Creation Timestamp:  2020-10-07T07:31:58Z
  Generation:          1
  Managed Fields:
    ...
  Resource Version:  1235740
  Self Link:         /apis/pmem-csi.intel.com/v1beta1/pmemcsideployments/pmem-csi.intel.com
  UID:               d8635490-53fa-4eec-970d-cd4c76f53b23
Spec:
  Device Mode:  lvm
  Node Selector:
    Storage:  pmem
Status:
  Conditions:
    Last Update Time:  2020-10-07T07:32:00Z
    Reason:            Driver certificates are available.
    Status:            True
    Type:              CertsReady
    Last Update Time:  2020-10-07T07:32:02Z
    Reason:            Driver deployed successfully.
    Status:            True
    Type:              DriverDeployed
  Driver Components:
    Component:     Controller
    Last Updated:  2020-10-08T07:45:13Z
    Reason:        1 instance(s) of controller driver is running successfully
    Status:        Ready
    Component:     Node
    Last Updated:  2020-10-08T07:45:11Z
    Reason:        All 3 node driver pod(s) running successfully
    Status:        Ready
  Last Updated:    2020-10-07T07:32:21Z
  Phase:           Running
  Reason:          All driver components are deployed successfully
Events:
  Type    Reason         Age   From               Message
  ----    ------         ----  ----               -------
  Normal  NewDeployment  58s   pmem-csi-operator  Processing new driver deployment
  Normal  Running        39s   pmem-csi-operator  Driver deployment successful

$ kubectl get pod -n pmem-csi
NAME                                             READY   STATUS    RESTARTS   AGE
pmem-csi-intel-com-controller-79cd9f799d-rt54d   2/2     Running   0          51s
pmem-csi-intel-com-node-4x7cv                    2/2     Running   0          50s
pmem-csi-intel-com-node-6grt6                    2/2     Running   0          50s
pmem-csi-intel-com-node-msgds                    2/2     Running   0          51s
pmem-csi-operator-749c7c7c69-k5k8n               1/1     Running   0          3m
```

#### Install via YAML files

- **Get source code**

PMEM-CSI uses Go modules and thus can be checked out and (if that should be desired)
built anywhere in the filesystem. Pre-built container images are available and thus
users don't need to build from source, but they may need some additional files
for the following sections.
To get the source code, use:

``` console
$ git clone https://github.com/intel/pmem-csi
```

- **Choose a namespace**

By default, setting up certificates as described in the next step will
automatically create a `pmem-csi` namespace if it does not exist yet.
Later the driver will be installed in that namespace.

This can be changed by:
- setting the `TEST_DRIVER_NAMESPACE` env variable to a different name
  when invoking `setup-ca-kubernetes.sh` and
- modifying the deployment with kustomize as explained below.

- **Set up certificates**

Certificates are required as explained in [Security](design.md#security) for
running the PMEM-CSI [scheduler extender](design.md#scheduler-extender) and
[webhook](design.md#pod-admission-webhook). If those are not used, then certificate
creation can be skipped. However, the YAML deployment files always create the PMEM-CSI
controller StatefulSet which needs the certificates. Without them, the
`pmem-csi-intel-com-controller` pod cannot start, so it is recommended to create
certificates or customize the deployment so that this Deployment is not created.

On OpenShift, certificates can be created automatically as described
in https://docs.openshift.com/container-platform/4.6/security/certificates/service-serving-certificate.html.
The PMEM-CSI operator uses that approach and therefore is a
simpler way to install PMEM-CSI on OpenShift.

Certificates can be created by running the `./test/setup-ca-kubernetes.sh` script for your cluster.
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

For example, to deploy for production with LVM device mode onto Kubernetes 1.18, use:

``` console
$ kubectl create -f deploy/kubernetes-1.18/pmem-csi-lvm.yaml
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
     namespace: pmem-driver
     bases:
       - ../deploy/kubernetes-1.18/lvm
     EOF
     $ kubectl create --kustomize my-pmem-csi-deployment
     ```

   - Configure how much PMEM is used by PMEM-CSI for LVM
     (see [Namespace modes in LVM device mode](design.md#namespace-modes-in-lvm-device-mode)):
     ``` ShellSession
     $ mkdir -p my-pmem-csi-deployment
     $ cat >my-pmem-csi-deployment/kustomization.yaml <<EOF
     bases:
       - ../deploy/kubernetes-1.18/lvm
     patchesJson6902:
       - target:
           group: apps
           version: v1
           kind: DaemonSet
           name: pmem-csi-node
           namespace: pmem-csi
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
$ kubectl get pods -n pmem-csi
NAME                                             READY   STATUS    RESTARTS   AGE
pmem-csi-intel-com-controller-79cd9f799d-rt54d   2/2     Running   0          3m15s
pmem-csi-intel-com-node-8kmxf                    2/2     Running   0          3m15s
pmem-csi-intel-com-node-bvx7m                    2/2     Running   0          3m15s
pmem-csi-intel-com-node-fbmpg                    2/2     Running   0          3m15s
```

### Volume parameters

A Kubernetes cluster administrators must define some volume parameters
like the filesystem type in [storage
classes](https://kubernetes.io/docs/concepts/storage/storage-classes).
Users then reference those storage classes when requesting generic
ephemeral inline or persistent volumes. The size of volumes can be chosen
by users.

`xfs` and `ext4` are supported filesystem types. In addition to the
normal parameters defined by Kubernetes, PMEM-CSI supports the
following custom parameters in a storage class:

|key|meaning|optional|values|
|---|-------|--------|-------------|
|`eraseAfter`|Clear all data by overwriting with zeroes after use and before deleting the volume|Yes|`true` (default), `false`|
|`kataContainers`|Prepare volume for use with DAX in Kata Containers.|Yes|`false/0/f/FALSE` (default), `true/1/t/TRUE`|
|`usage`|Determine how a volume is going to be used.|Yes|`AppDirect` (default), `FileIO`|

By default, volumes are created for AppDirect enabled applications:
- The [namespace
  mode](https://docs.pmem.io/ndctl-user-guide/concepts/nvdimm-namespaces) is
  `fsdax`.
- Mount parameters include `-o dax` (= [`-o dax=always` on newer kernels](https://www.kernel.org/doc/Documentation/filesystems/dax.txt))
  which ensures that all files are automatically opened in DAX mode, i.e.
  reads and writes directly access the underlying PMEM.

This might not be ideal for traditional file IO because the page cache is
bypassed, which may affect performance, and because applications have to be
prepared to deal with partially written data sectors in case of crashes. When
the goal is to run traditional applications, then `usage=FileIO` may be better:
- In direct mode, the [namespace
  mode](https://docs.pmem.io/ndctl-user-guide/concepts/nvdimm-namespaces) is
  `sector`.
- In LVM mode, the namespace mode is `fsdax` because currently
  PMEM-CSI doesn't support LVM on top of other namespaces.
- Mount parameters do not include `-o dax`.

`kataContainers` and `usage=FileIO` are mutually exclusive because the former
is about making AppDirect available in Kata Containers. The normal volume
passthrough can be used for `usage=FileIO`.

### Creating volumes

This section uses files from the [common example directory](/deploy/common).
It is not necessary to check out the repository to use them.

Create a storage class with late binding, the recommended mode:
``` console
$ kubectl apply -f https://github.com/intel/pmem-csi/raw/devel/deploy/common/pmem-storageclass-late-binding.yaml
storageclass.storage.k8s.io/pmem-csi-sc-late-binding created
```

Then request a volume which uses that class:
``` console
$ kubectl apply -f https://github.com/intel/pmem-csi/raw/devel/deploy/common/pmem-pvc-late-binding.yaml
persistentvolumeclaim/pmem-csi-pvc-late-binding created
```

At this point, the volume is not yet getting created because of the
late binding mode:
``` console
$ kubectl describe pvc/pmem-csi-pvc-late-binding
Name:          pmem-csi-pvc-late-binding
Namespace:     default
StorageClass:  pmem-csi-sc-late-binding
Status:        Pending
Volume:        
Labels:        <none>
Annotations:   <none>
Finalizers:    [kubernetes.io/pvc-protection]
Capacity:      
Access Modes:  
VolumeMode:    Filesystem
Used By:       <none>
Events:
  Type    Reason                Age               From                         Message
  ----    ------                ----              ----                         -------
  Normal  WaitForFirstConsumer  0s (x2 over 14s)  persistentvolume-controller  waiting for first consumer to be created before binding
```

The volume gets created once the first Pod starts to use it, on a node that is suitable for that Pod:
``` console
$ kubectl apply -f https://github.com/intel/pmem-csi/raw/devel/deploy/common/pmem-app-late-binding.yaml
pod/my-csi-app created
```

After a short while, the volume is created and the pod can run:
``` console
$ kubectl get pvc,pods -o wide
NAME                                              STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS               AGE
persistentvolumeclaim/pmem-csi-pvc-late-binding   Bound    pvc-ade8dc48-a4c0-4f30-b479-84460a3e0591   4Gi        RWO            pmem-csi-sc-late-binding   55s

NAME             READY   STATUS    RESTARTS   AGE
pod/my-csi-app   1/1     Running   0          47s
```

The volume was mounted with `dax=always`, therefore all file operations and memory regions mapped from that
volume into the address space of an application directly access the underlying PMEM:
``` console
$ kubectl exec my-csi-app -- df /data
Filesystem                                                                              1K-blocks  Used Available Use% Mounted on
/dev/ndbus0region0fsdax/pvc-7d-83241976933418f96748a1c18d500c6cba91c1dfaa87145b7893569c   4062912 16376   3820440   1% /data

$ kubectl exec my-csi-app -- mount |grep /data
/dev/ndbus0region0fsdax/pvc-7d-83241976933418f96748a1c18d500c6cba91c1dfaa87145b7893569c on /data type ext4 (rw,relatime,seclabel,dax=always)
```

### Troubleshooting

A few things can go wrong when trying out the previous example.

#### Driver or operator fails

This shows up in `kubectl get pods --all-namespaces` as failed Pods
and can be investigated with `kubectl describe --namespace <driver
namespace> pods/<pod name>` and `kubectl logs --namespace <driver
namespace> <pod name> pmem-driver` or one of the other containers
in that Pod.

When using deployment files from the `devel` branch, the corresponding
container `canary` image might not have been published yet. Better use
the [latest stable release](https://intel.github.io/pmem-csi/).

#### No driver Pod created for a node

This can be checked with `kubectl get pods --all-namespaces -o wide`.
Have nodes been labeled as expected by the driver deployment? Check
with `kubectl get nodes -o yaml`.

#### Example Pod getting assigned to a node with no PMEM

This can happen on clusters where only some worker nodes have PMEM and
the [PMEM-CSI scheduler extensions](#enable-scheduler-extensions) are
not enabled. This can be checked by looking at the `selected-node`
annotation of the PVC:
``` console
$ kubectl get pvc/pmem-csi-pvc-late-binding -o yaml | grep ' volume.kubernetes.io/selected-node:'
    volume.kubernetes.io/selected-node: pmem-csi-pmem-govm-worker2
```

The PMEM-CSI controller pod will detect this and ask the scheduler to
pick a node anew by removing that annotation, but it is random whether
the next choice is better and starting the Pod may get delayed.

To avoid this, enable the scheduler extensions.

#### Example Pod getting assigned to a node with insufficient PMEM

This also can only happen when the [PMEM-CSI scheduler
extensions](#enable-scheduler-extensions) are not enabled. Then volume
creation is attempted repeatedly, potentially on different nodes, but
fails with `not enough space` errors:

``` console
$ kubectl describe pvc/pmem-csi-pvc-late-binding
Name:          pmem-csi-pvc-late-binding
Namespace:     default
StorageClass:  pmem-csi-sc-late-binding
Status:        Pending
Volume:        
Labels:        <none>
Annotations:   volume.beta.kubernetes.io/storage-provisioner: pmem-csi.intel.com
Finalizers:    [kubernetes.io/pvc-protection]
Capacity:      
Access Modes:  
VolumeMode:    Filesystem
Used By:       my-csi-app
Events:
  Type     Reason                Age                     From                                                                                   Message
  ----     ------                ----                    ----                                                                                   -------
  Normal   WaitForFirstConsumer  7m30s                   persistentvolume-controller                                                            waiting for first consumer to be created before binding
  Normal   WaitForPodScheduled   6m (x15 over 7m19s)     persistentvolume-controller                                                            waiting for pod my-csi-app to be scheduled
  Warning  ProvisioningFailed    3m59s (x12 over 7m19s)  pmem-csi.intel.com_pmem-csi-intel-com-node-nwkqv_cc2984e6-915f-4cf2-93a0-e143da407917  failed to provision volume with StorageClass "pmem-csi-sc-late-binding": rpc error: code = ResourceExhausted desc = Node CreateVolume: device creation failed: not enough space
  Warning  ProvisioningFailed    2m47s (x12 over 7m18s)  pmem-csi.intel.com_pmem-csi-intel-com-node-9vlhf_6ac47898-58bf-45e1-b601-5d8f39d21f4e  failed to provision volume with StorageClass "pmem-csi-sc-late-binding": rpc error: code = ResourceExhausted desc = Node CreateVolume: device creation failed: not enough space
  Normal   ExternalProvisioning  2m23s (x28 over 7m19s)  persistentvolume-controller                                                            waiting for a volume to be created, either by external provisioner "pmem-csi.intel.com" or manually created by system administrator
  Normal   Provisioning          2m11s (x14 over 7m18s)  pmem-csi.intel.com_pmem-csi-intel-com-node-9vlhf_6ac47898-58bf-45e1-b601-5d8f39d21f4e  External provisioner is provisioning volume for claim "default/pmem-csi-pvc-late-binding"
  Normal   Provisioning          107s (x16 over 7m19s)   pmem-csi.intel.com_pmem-csi-intel-com-node-nwkqv_cc2984e6-915f-4cf2-93a0-e143da407917  External provisioner is provisioning volume for claim "default/pmem-csi-pvc-late-binding"
```

The scheduler extensions prevent these useless attempts on nodes with
insufficient PMEM. When none of the available nodes have sufficient
PMEM, the attempt to schedule the example Pod fails:

``` console
$ kubectl describe pod/my-csi-app
Name:         my-csi-app
Namespace:    default
...
Events:
  Type     Reason            Age                From               Message
  ----     ------            ----               ----               -------
  Warning  FailedScheduling  12s (x2 over 12s)  default-scheduler  0/4 nodes are available: 1 node(s) had taint {node-role.kubernetes.io/master: }, that the pod didn't tolerate, 3 only 63484MiB of PMEM available, need 400GiB.
```

#### Less PMEM available than expected

This is usually the result of not preparing the node(s) as describe in
[persistent memory
pre-provisioning](#persistent-memory-pre-provisioning).

One way of checking this is to look at the logs of the PMEM-CSI driver
on a node. In this case, `region0` was completely unused and the
driver was configured to use 50% of that for an LVM volume group:
``` console
$ kubectl get pods --all-namespaces -l app.kubernetes.io/name=pmem-csi-node -o wide
NAMESPACE   NAME                            READY   STATUS    RESTARTS   AGE   IP                NODE                         NOMINATED NODE   READINESS GATES
pmem-csi    pmem-csi-intel-com-node-d2mfh   3/3     Running   0          75s   192.168.200.66    pmem-csi-pmem-govm-worker3   <none>           <none>
pmem-csi    pmem-csi-intel-com-node-jkbgz   3/3     Running   0          75s   192.168.133.134   pmem-csi-pmem-govm-worker1   <none>           <none>
pmem-csi    pmem-csi-intel-com-node-th56d   3/3     Running   0          75s   192.168.220.67    pmem-csi-pmem-govm-worker2   <none>           <none>

$ kubectl logs -n pmem-csi pmem-csi-intel-com-node-jkbgz pmem-driver
I0623 07:15:18.710690       1 main.go:73] "PMEM-CSI started." version="v0.9.0-188-gd451ec6f3-dirty"
I0623 07:15:18.711645       1 pmd-lvm.go:328] "LVM-New/setupNS: Checking region for fsdax namespaces" region="region0" percentage=50 size="64Gi" available="64Gi" max-available-extent="64Gi" may-use="32Gi"
I0623 07:15:18.712251       1 pmd-lvm.go:361] "LVM-New/setupNS: Create fsdax namespace" size="32Gi"
I0623 07:15:19.041186       1 region.go:282] "LVM-New/setupNS/CreateNamespace: Namespace created" region="region0" namespace="namespace0.1" usable-size="32254Mi" raw-size="32Gi" uuid="c3e6fe52-d3f2-11eb-b33e-c2b1549139a7"
I0623 07:15:19.079791       1 pmd-lvm.go:422] "LVM-New/setupVG/setupVGForNamespace: Creating new volume group" vg="ndbus0region0fsdax"
I0623 07:15:19.130041       1 mount_linux.go:163] Detected OS without systemd
I0623 07:15:19.130661       1 server.go:54] "GRPC Server: Listening for connections" endpoint="unix:///csi/csi.sock"
I0623 07:15:19.180760       1 pmem-csi-driver.go:305] "PMEM-CSI ready." capacity="32252Mi maximum volume size, 32252Mi available, 32252Mi managed, 64Gi total"
```

In a production environment, the [metrics support](#metrics-support)
could be used to monitor available PMEM per node.

### Automatic node setup

The expectation is that the scripts which bring up nodes can be
adapted to prepare the PMEM for usage by PMEM-CSI as explained
earlier. But this might not always be easy.

For the case of converting an existing "raw" namespace to "fsdax" mode
there is a possibility to do the conversion through a deployed
PMEM-CSI driver:

1. Install PMEM-CSI in LVM mode without preparing nodes. At this point
   only the central controller pod will run.
1. For each node that has one or more raw namespaces that all need
   to be converted, set the `<driver name>/convert-raw-namespaces` label (usually
   `pmem-csi.intel.com/convert-raw-namespaces`) to `force`.
1. This will cause the pods of the `pmem-csi-intel-com-node-setup`
   DaemonSet to run on those nodes. Those pods then will
   convert the namespaces, create the LVM volume group,
   remove the `convert-raw-namespaces` label
   (i.e. the pods will only run once) and add the normal label
   that enables the PMEM-CSI node driver pods to run.
1. The normal node driver pods start up and then are
   ready to provision volumes.

**WARNING**: the raw namespaces will be converted even when they are
active. If data was stored on them, it will be lost after the
conversion.

The output of a successful conversion will look like this:
```
I0623 07:32:52.773207       1 main.go:73] "PMEM-CSI started." version="v0.9.0-188-gd451ec6f3-dirty"
I0623 07:32:52.774386       1 convert.go:79] "ForceConvertRawNamespaces/convert: checking for namespaces"
I0623 07:32:52.774871       1 convert.go:81] "ForceConvertRawNamespaces/convert: checking" bus="{\"dev\":\"ndbus0\",\"dimms\":[{}],\"provider\":\"ACPI.NFIT\",\"regions\":[{}]}"
I0623 07:32:52.775316       1 convert.go:83] "ForceConvertRawNamespaces/convert: checking" region="{\"available_size\":0,\"dev\":\"region0\",\"mappings\":[{}],\"max_available_extent\":0,\"namespaces\":[{}],\"size\":68719476736,\"type\":\"pmem\"}"
I0623 07:32:52.775444       1 convert.go:90] "ForceConvertRawNamespaces/convert: checking" namespace="{\"blockdev\":\"pmem0\",\"dev\":\"namespace0.0\",\"enabled\":true,\"id\":0,\"mode\":\"raw\",\"name\":\"\",\"size\":68719476736,\"uuid\":\"1711a2a0-358d-4b14-a43c-8efa1a9f7154\"}"
I0623 07:32:52.775550       1 convert.go:99] "ForceConvertRawNamespaces/convert: converting raw namespace" namespace="{\"blockdev\":\"pmem0\",\"dev\":\"namespace0.0\",\"enabled\":true,\"id\":0,\"mode\":\"raw\",\"name\":\"\",\"size\":68719476736,\"uuid\":\"1711a2a0-358d-4b14-a43c-8efa1a9f7154\"}"
I0623 07:32:53.397897       1 convert.go:127] "ForceConvertRawNamespaces/convert: setting up volume group" namespace="{\"blockdev\":\"pmem0\",\"dev\":\"namespace0.0\",\"enabled\":false,\"id\":0,\"mode\":\"fsdax\",\"name\":\"\",\"size\":18446744073709551615,\"uuid\":\"00000000-0000-0000-0000-000000000000\"}" vg="ndbus0region0fsdax"
I0623 07:32:53.434094       1 pmd-lvm.go:422] "ForceConvertRawNamespaces/convert/setupVGForNamespace: Creating new volume group" vg="ndbus0region0fsdax"
I0623 07:32:53.457108       1 convert.go:133] "ForceConvertRawNamespaces/convert: converted to fsdax namespace" namespace="{\"blockdev\":\"pmem0\",\"dev\":\"namespace0.0\",\"enabled\":false,\"id\":0,\"mode\":\"fsdax\",\"name\":\"\",\"size\":18446744073709551615,\"uuid\":\"00000000-0000-0000-0000-000000000000\"}" vg="ndbus0region0fsdax"
I0623 07:32:53.457148       1 convert.go:75] "ForceConvertRawNamespaces/convert: successful" converted=1
I0623 07:32:53.479512       1 convert.go:172] "ForceConvertRawNamespaces/havePMEM: Volume group will be used by PMEM-CSI in LVM mode" vg="ndbus0region0fsdax"
I0623 07:32:53.523412       1 convert.go:200] "ForceConvertRawNamespaces/relabel: Change node labels" node="pmem-csi-pmem-govm-master" patch="{\"metadata\":{\"labels\":{\"pmem-csi.intel.com/convert-raw-namespaces\": null, \"feature.node.kubernetes.io/memory-nv.dax\": \"true\"}}}"
I0623 07:32:53.523605       1 pmem-csi-driver.go:326] "Raw namespace conversion is done, waiting for termination signal."
I0623 07:33:03.954098       1 pmem-csi-driver.go:344] "Caught signal, terminating." signal="terminated"
I0623 07:33:05.016426       1 main.go:93] "PMEM-CSI stopped."
```

It terminates once Kubernetes notices that the pod is no longer
needed. This usually happens quickly, so a log monitoring solution
may be needed to see this output because `kubectl logs` does not work
for pods that were already deleted.

The DaemonSet contains some information which is available longer:
```console
$ kubectl describe daemonsets/pmem-csi-intel-com-node-setup
Name:           pmem-csi-intel-com-node-setup
Selector:       app.kubernetes.io/instance=pmem-csi.intel.com,app.kubernetes.io/name=pmem-csi-node-setup,pmem-csi.intel.com/deployment=lvm-production
Node-Selector:  pmem-csi.intel.com/convert-raw-namespaces=force
Labels:         app.kubernetes.io/component=node-setup
                app.kubernetes.io/instance=pmem-csi.intel.com
                app.kubernetes.io/name=pmem-csi-node-setup
                app.kubernetes.io/part-of=pmem-csi
                pmem-csi.intel.com/deployment=lvm-production
Annotations:    deprecated.daemonset.template.generation: 1
Desired Number of Nodes Scheduled: 0
Current Number of Nodes Scheduled: 0
Number of Nodes Scheduled with Up-to-date Pods: 0
Number of Nodes Scheduled with Available Pods: 0
Number of Nodes Misscheduled: 0
Pods Status:  0 Running / 0 Waiting / 0 Succeeded / 0 Failed
Pod Template:
  Labels:           app.kubernetes.io/component=node-setup
                    app.kubernetes.io/instance=pmem-csi.intel.com
                    app.kubernetes.io/name=pmem-csi-node-setup
                    app.kubernetes.io/part-of=pmem-csi
                    pmem-csi.intel.com/deployment=lvm-production
                    pmem-csi.intel.com/webhook=ignore
  Service Account:  pmem-csi-intel-com-node-setup
  Containers:
   pmem-driver:
    Image:      172.17.42.1:5001/pmem-csi-driver:canary
    Port:       <none>
    Host Port:  <none>
    Command:
      /usr/local/bin/pmem-csi-driver
      -v=3
      -logging-format=text
      -mode=force-convert-raw-namespaces
      -nodeSelector={"storage":"pmem"}
      -nodeid=$(KUBE_NODE_NAME)
...
Events:
  Type    Reason            Age    From                  Message
  ----    ------            ----   ----                  -------
  Normal  SuccessfulCreate  5m47s  daemonset-controller  Created pod: pmem-csi-intel-com-node-setup-fr9b8
  Normal  SuccessfulDelete  5m45s  daemonset-controller  Deleted pod: pmem-csi-intel-com-node-setup-fr9b8
```

If conversion fails, the pod will exit with an error and then get
restarted automatically by Kubernetes to retry the conversion until it
succeeds.

It is considered a user error if conversion is requested for a node
which has nothing to convert. To make that obvious, the pod will print
an error and then exist with an error. That way, the pod continues to
exist and the log can be inspected to identify the problem.



### Kata Containers support

[Kata Containers support](design.md#kata-containers-support) gets enabled via
the `kataContainers` storage class parameter. PMEM-CSI then
creates a filesystem inside a partition inside a file. When such a volume
is used inside Kata Containers, the Kata Containers runtime makes sure that
the filesystem is mounted on an emulated NVDIMM device with full DAX support.

On the host, PMEM-CSI will try to mount through a loop device with `-o
dax` but proceed without `-o dax` when the kernel does not support
that. Currently Linux up to and including 5.4 do not support it and it
is unclear when that support will be added In other words, on the host
such volumes are usable, but only without DAX.

When disabled, volumes support DAX on the host and are usable without
DAX inside Kata Containers.

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
annotation](https://github.com/kata-containers/documentation/blob/master/how-to/how-to-set-sandbox-config-kata.md#hypervisor-options)
in the pod meta data. In both cases, the value has to be large enough
for all PMEM volumes used by the pod, otherwise pod creation will fail
with an error similar to this:

``` console
Error: container create failed: QMP command failed: not enough space, currently 0x8000000 in use of total space for memory devices 0x3c100000
```

**Note**:
* The offset is currently (= Kata Containers 2.1.0) limited to
32 bit, which implies that volumes cannot be larger than 4GiB. An
enhancement request for [Kata Containers is
pending](https://github.com/kata-containers/kata-containers/issues/2006).
* A newer version is also needed for a fix of [issue
#2018](https://github.com/kata-containers/kata-containers/issues/2018).
* kata-deploy, at least in Kata Containers 2.1.0, does [not enable the
  `memory_offset` annotation](https://github.com/kata-containers/kata-containers/issues/2088), leading to
  `failed to create containerd task: annotation io.katacontainers.config.hypervisor.memory_offset is not enabled`
  errors.

The examples for usage of Kata Containers [with
ephemeral](/deploy/common/pmem-kata-app-ephemeral.yaml) and
[persistent](/deploy/common/pmem-kata-app.yaml) volumes use the pod
label. They assume that the `kata-qemu` runtime class [is
installed](https://github.com/kata-containers/kata-containers/tree/2.1.0/tools/packaging/kata-deploy#run-a-sample-workload).

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

Volume requests [embedded in the pod spec with the `csi` field](https://kubernetes-csi.github.io/docs/ephemeral-local-volumes.html) are provisioned as
ephemeral volumes. The volume request could use below fields as
[`volumeAttributes`](https://kubernetes.io/docs/concepts/storage/volumes/#csi):

|key|meaning|optional|values|
|---|-------|--------|-------------|
|`size`|Size of the requested ephemeral volume as [Kubernetes memory string](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#meaning-of-memory) ("1Mi" = 1024*1024 bytes, "1e3K = 1000000 bytes)|No||
|`eraseAfter`|Clear all data by overwriting with zeroes after use and before deleting the volume|Yes|`true` (default), `false`|
|`kataContainers`|Prepare volume for use in Kata Containers.|Yes|`false/0/f/FALSE` (default), `true/1/t/TRUE`|

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

#### Manual scheduler configuration

**NOTE**: this sections provides an in-depth explanation that makes no
assumptions about how the cluster works. For simpler install
instructions on OpenShift see [below](#openshift-scheduler-configuration).

The PMEM-CSI scheduler extender and admission webhook are provided by
the PMEM-CSI controller. They need to be enabled during deployment via
the `--schedulerListen=[<listen address>]:<port>` parameter. The
listen address is optional and can be left out. The port is where a
HTTPS server will run. The YAML files already enable this. The
operator has the `controllerTLSSecret` and `mutatePods` properties in
the [`DeploymentSpec`](#deploymentspec).

The controller needs TLS certificates which must be created in
advance. The YAML files expects them in a secret called
`pmem-csi-intel-com-controller-secret` and will not work without one.
The operator is more flexible and creates a driver without the
controller by default. This can be changed by setting the
`controllerTLSSecret` field in the `PmemCSIDeployment` API.

That secret must contain the following data items:
- `ca.crt`: root CA certificate
- `tls.key`: secret key of the webhook
- `tls.crt`: public key of the webhook

The webhook certificate must include host names that match how the
webhooks are going to be called by the `kube-apiserver`
(i.e. `pmem-csi-intel-com-scheduler.pmem-csi.svc` for a
deployment with the `pmem-csi.intel.com` driver name in the `pmem-csi`
namespace) and by the `kube-scheduler` (might be the same service name, through some external
load balancer or `127.0.0.1` when using the node port workaround
described below).

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
      name: pmem-csi-intel-com-scheduler
      namespace: pmem-csi
    path: node-port-patch.yaml
EOF

$ cat >my-scheduler/node-port-patch.yaml <<EOF
- op: add
  path: /spec/ports/0/nodePort
  value: 32000
- op: add
  path: /spec/type
  value: NodePort
EOF

$ kubectl create --kustomize my-scheduler
```

When the node port is not needed, the scheduler service can be
created directly with:
``` console
kubectl create --kustomize deploy/kustomize/scheduler
```

How to (re)configure `kube-scheduler` depends on the cluster. With
kubeadm it is possible to set all necessary options in advance before
creating the master node with `kubeadm init`. A running cluster can
be modified with `kubeadm upgrade`.

One additional
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
      version: v1
      kind: MutatingWebhookConfiguration
      name: pmem-csi-intel-com-hook
    path: webhook-patch.yaml
EOF

$ cat >my-webhook/webhook-patch.yaml <<EOF
- op: replace
  path: /webhooks/0/clientConfig/caBundle
  value: $(base64 -w 0 _work/pmem-ca/ca.pem)
EOF

$ kubectl create --kustomize my-webhook
```

#### OpenShift scheduler configuration

**NOTE**: The scheduler extensions are only needed on OpenShift 4.6
and 4.7. On OpenShift 4.8, [storage capacity
tracking](#storage-capacity-tracking) can and should be used instead.

The operator should be used on OpenShift. When creating the
deployment, set `controllerTLSSecret` to the special string
`-openshift-`:

``` ShellSession
$ kubectl create -f - <<EOF
apiVersion: pmem-csi.intel.com/v1beta1
kind: PmemCSIDeployment
metadata:
  name: pmem-csi.intel.com
spec:
  deviceMode: lvm
  nodeSelector:
    feature.node.kubernetes.io/memory-nv.dax: "true"
  controllerTLSSecret: -openshift-
EOF
```

The webhook and the API server then get configured by the operator
with [certificates created automatically by
OpenShift](https://docs.openshift.com/container-platform/4.6/security/certificates/service-serving-certificate.html).

The scheduler must be configured manually, using the same API as for
[configuring scheduler
policies](https://docs.openshift.com/container-platform/4.6/nodes/scheduling/nodes-scheduler-default.html#nodes-scheduler-default-modifying_nodes-scheduler-default). This
can be done before or after deploying the PMEM-CSI driver. The
configuration change can be left in place after removing a PMEM-CSI
because it will then be ignored. However, without this step pods that
use PMEM-CSI volumes will not get scheduled.

Communication between the kube-scheduler and PMEM-CSI will be done via
http and a service that listens on a dynamically allocated host
port. This approach is necessary because:
- kube-scheduler uses the host network and thus cannot connect to a
  service that is only available inside the cluster and
- There is no API for configuring TLS certificates.

First, define the service inside the namespace where the PMEM-CSI
operator runs:

``` ShellSession
oc apply -f - <<EOF
apiVersion: v1
kind: Service
metadata:
  name: pmem-csi-intel-com-http-scheduler
  namespace: pmem-csi
spec:
  selector:
    app.kubernetes.io/name: pmem-csi-controller
    app.kubernetes.io/instance: pmem-csi.intel.com # This must be the name of the PMEM-CSI deployment.
  type: NodePort
  ports:
  - targetPort: 8001
    port: 80
EOF
```

Then create a scheduler policy. If such a policy already exists, the
`extenders` section below must be added to it.

``` ShellSession
oc create -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: scheduler-policy
  namespace: openshift-config
data:
  policy.cfg: |
    {
      "kind" : "Policy",
      "apiVersion" : "v1",
      "extenders" : [
        { "urlPrefix": "http://127.0.0.1:$(oc get service/pmem-csi-intel-com-http-scheduler -n pmem-csi -o jsonpath={.spec.ports[*].nodePort})",
          "filterVerb": "filter",
          "prioritizeVerb": "prioritize",
          "nodeCacheCapable": true,
          "weight": 1,
          "managedResources": [
            { "name": "pmem-csi.intel.com/scheduler",
              "ignoredByScheduler": true
            }
          ]
        }
      ]
    }
EOF
```

Finally, activate the usage of that policy by updating the existing
`scheduler/cluster` object. If a policy was set already, this command
will fail with `The request is invalid`, in which case the existing
policy config map must be edited.

``` console
$ oc patch scheduler/cluster --type json \
   --patch '[{"op":"test","path":"/spec/policy/name","value":""}, {"op":"replace","path":"/spec/policy/name","value":"scheduler-policy"}]'
scheduler.config.openshift.io/cluster patched
```

This causes schedulers to be restarted with a new configuration:

``` console
$ oc get events -n openshift-kube-scheduler-operator
...
14m         Normal    ConfigMapCreated                    deployment/openshift-kube-scheduler-operator               Created ConfigMap/policy-configmap -n openshift-kube-scheduler because it was missing
14m         Normal    RevisionTriggered                   deployment/openshift-kube-scheduler-operator               new revision 7 triggered by "configmap/policy-configmap has changed"
14m         Normal    ConfigMapCreated                    deployment/openshift-kube-scheduler-operator               Created ConfigMap/revision-status-7 -n openshift-kube-scheduler because it was missing
14m         Normal    ConfigMapCreated                    deployment/openshift-kube-scheduler-operator               Created ConfigMap/kube-scheduler-pod-7 -n openshift-kube-scheduler because it was missing
...
13m         Normal    OperatorStatusChanged               deployment/openshift-kube-scheduler-operator               Status for clusteroperator/kube-scheduler changed: Progressing changed from True to False ("NodeInstallerProgressing: 1 nodes are at revision 8"),Available message changed from "StaticPodsAvailable: 1 nodes are active; 1 nodes are at revision 6; 0 nodes have achieved new revision 8" to "StaticPodsAvailable: 1 nodes are active; 1 nodes are at revision 8"
13m         Normal    ConfigMapUpdated                    deployment/openshift-kube-scheduler-operator               Updated ConfigMap/revision-status-8 -n openshift-kube-scheduler:
cause by changes in data.status
13m         Normal    PodCreated                          deployment/openshift-kube-scheduler-operator               Created Pod/revision-pruner-8-tt-87fkd-master-0 -n openshift-kube-scheduler because it was missing

$ oc get pods -n openshift-kube-scheduler -l app=openshift-kube-scheduler
NAME                                         READY   STATUS    RESTARTS   AGE
openshift-kube-scheduler-tt-87fkd-master-0   3/3     Running   0          11m

$ oc exec -ti -n openshift-kube-scheduler openshift-kube-scheduler-tt-87fkd-master-0 -c kube-scheduler -- cat /etc/kubernetes/static-pod-resources/configmaps/policy-configmap/policy.cfg
{
  "kind" : "Policy",
  "apiVersion" : "v1",
  "extenders" : [
    { "urlPrefix": "https://127.0.0.1:30674",
      "filterVerb": "filter",
      "prioritizeVerb": "prioritize",
      "nodeCacheCapable": true,
      "weight": 1,
      "managedResources": [
        { "name": "pmem-csi.intel.com/scheduler",
          "ignoredByScheduler": true
        }
      ]
    }
  ]
}
```

### Storage capacity tracking

[Kubernetes
1.19](https://kubernetes.io/blog/2020/09/01/ephemeral-volumes-with-storage-capacity-tracking/)
introduces support for publishing and using storage capacity
information for pod scheduling. It became beta in 1.21. PMEM-CSI must be deployed
differently to use this feature:

- `external-provisioner` must be told to publish storage capacity
  information via command line arguments.
- A flag in the CSI driver information must be set for the Kubernetes
  scheduler, otherwise it ignores that information when considering
  pods with unbound volume.

The deployments for Kubernetes >= 1.21 do this automatically. The
alpha API in 1.19 and 1.20 is no longer supported.


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
`scheduler_request_duration_seconds` | histogram | Latencies for PMEM-CSI scheduler HTTP requests by operation ("mutate", "filter", "status") and method ("post").
`scheduler_in_flight_requests` | gauge | Currently pending PMEM-CSI scheduler HTTP requests.
`scheduler_requests_total` | counter | Number of HTTP requests to the PMEM-CSI scheduler, regardless of operation and method.
`scheduler_response_size_bytes` | histogram | Histogram of response sizes for PMEM-CSI scheduler requests, regardless of operation and method.
`csi_[sidecar\|plugin]_operations_seconds` | histogram | gRPC call duration and error code, for sidecar to driver (aka plugin) communication.
`go_*` | | [Go runtime information](https://github.com/prometheus/client_golang/blob/master/prometheus/go_collector.go)
`pmem_amount_available` | gauge | Remaining amount of PMEM on the host that can be used for new volumes.
`pmem_amount_managed` | gauge | Amount of PMEM on the host that is managed by PMEM-CSI.
`pmem_amount_max_volume_size` | gauge | The size of the largest PMEM volume that can be created.
`pmem_amount_total` | gauge | Total amount of PMEM on the host.
`process_*` | | [Process information](https://github.com/prometheus/client_golang/blob/master/prometheus/process_collector.go)
`promhttp_metric_handler_requests_in_flight` | gauge | Current number of scrapes being served.
`promhttp_metric_handler_requests_total` | counter | Total number of scrapes by HTTP status code.

This list is tentative and may still change as long as metrics support
is alpha. To see all available data, query a container. Different
containers provide different data. For example, the controller
provides:

``` ShellSession
$ kubectl port-forward -n pmem-csi $(kubectl get pods -n pmem-csi -o name -l app.kubernetes.io/name=pmem-csi-controller) 10010
Forwarding from 127.0.0.1:10010 -> 10010
Forwarding from [::1]:10010 -> 10010
```

And in another shell:

``` ShellSession
$ curl --silent http://localhost:10010/metrics | grep '# '
# HELP build_info A metric with a constant '1' value labeled by version.
# TYPE build_info gauge
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


```console
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

`PmemCSIDeployment` is a cluster-scoped Kubernetes resource in the
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

Although the operator allows running multiple PMEM-CSI driver deployments, one
has to take extreme care of such deployments by ensuring that not more than
one driver ends up running on the same node(s). Nodes on which a PMEM-CSI
driver could run can be configured by using the `nodeSelector` property of
the `DeploymentSpec`.

**NOTE**: Starting from release v0.9.0 reconciling of the `Deployment`
CRD in `pmem-csi.intel.com/v1alpha1` API group is not supported by the
PMEM-CSI operator anymore. Such resources in the cluster must be migrated
manually to new the `PmemCSIDeployment` API.


The current API for `PmemCSIDeployment` resources is:

|Field | Type | Description |
|---|---|---|
| apiVersion | string  | `pmem-csi.intel.com/v1beta1` |
| kind | string | `PmemCSIDeployment`|
| metadata | [ObjectMeta](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#metadata) | Object metadata, name used for CSI driver and as prefix for sub-objects |
| spec | [DeploymentSpec](#deployment-spec) | Specification of the desired behavior of the deployment |

### DeploymentSpec

Below specification fields are valid in all API versions unless noted otherwise in the description.

The default values are used by the operator when no value is set for a
field explicitly. Those defaults can change over time and are not part
of the API specification.

|Field | Type | Description | Default Value |
|---|---|---|---|
| image | string | PMEM-CSI docker image name used for the deployment | the same image as the operator<sup>1</sup> |
| provisionerImage | string | [CSI provisioner](https://kubernetes-csi.github.io/docs/external-provisioner.html) docker image name | latest [external provisioner](https://kubernetes-csi.github.io/docs/external-provisioner.html) stable release image<sup>2</sup> |
| nodeRegistrarImage | string | [CSI node driver registrar](https://github.com/kubernetes-csi/node-driver-registrar) docker image name | latest [node driver registrar](https://kubernetes-csi.github.io/docs/node-driver-registrar.html) stable release image<sup>2</sup> |
| pullPolicy | string | Docker image pull policy. either one of `Always`, `Never`, `IfNotPresent` | `IfNotPresent` |
| logLevel | integer | PMEM-CSI driver logging level | 3 |
| logFormat | text | log output format | "text" or "json" <sup>3</sup> |
| deviceMode | string | Device management mode to use. Supports one of `lvm` or `direct` | `lvm`
| controllerTLSSecret | string | Name of an existing secret in the driver's namespace which contains ca.crt, tls.crt and tls.key data for the scheduler extender and pod mutation webhook. A controller is started if (and only if) this secret is specified. <br> Alternatively, the special string `-openshift-` can be used on OpenShift to let OpenShift create the necessary secrets. | empty
| controllerReplicas | int | Number of concurrently running controller pods. | 1
| mutatePods | Always/Try/Never | Defines how a mutating pod webhook is configured if a controller is started. The field is ignored if the controller is not enabled. "Never" disables pod mutation. "Try" configured it so that pod creation is allowed to proceed even when the webhook fails. "Always" requires that the webhook gets invoked successfully before creating a pod. | Try
| schedulerNodePort | int or string | If non-zero, the scheduler service is created as a NodeService with that fixed port number. Otherwise that service is created as a cluster service. The number must be from the range reserved by Kubernetes for node ports. This is useful if the kube-scheduler cannot reach the scheduler extender via a cluster service. | 0
| controllerResources | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.12/#resourcerequirements-v1-core) | Describes the compute resource requirements for controller pod. <br/><sup>4</sup>_Deprecated and only available in `v1alpha1`._ |
| nodeResources | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.12/#resourcerequirements-v1-core) | Describes the compute resource requirements for the pods running on node(s). <br/>_<sup>4</sup>Deprecated and only available in `v1alpha1`._ |
| controllerDriverResources | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.12/#resourcerequirements-v1-core) | Describes the compute resource requirements for controller driver container running on master node. Available since `v1beta1`. |
| nodeDriverResources | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.12/#resourcerequirements-v1-core) | Describes the compute resource requirements for the driver container running on worker node(s). <br/>_Available since `v1beta1`._ |
| provisionerResources | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.12/#resourcerequirements-v1-core) | Describes the compute resource requirements for the [external provisioner](https://kubernetes-csi.github.io/docs/external-provisioner.html) sidecar container. _Available since `v1beta1`._ |
| nodeRegistrarResources | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.12/#resourcerequirements-v1-core) | Describes the compute resource requirements for the [driver registrar](https://kubernetes-csi.github.io/docs/node-driver-registrar.html) sidecar container running on worker node(s). <br/>_Available since `v1beta1`._ |
| registryCert | string | Encoded tls certificate signed by a certificate authority used for driver's controller registry server | generated by operator self-signed CA |
| nodeControllerCert | string | Encoded tls certificate signed by a certificate authority used for driver's node controllers | generated by operator self-signed CA |
| registryKey | string | Encoded RSA private key used for signing by `registryCert` | generated by the operator |
| nodeControllerKey | string | Encoded RSA private key used for signing by `nodeControllerCert` | generated by the operator |
| caCert | string | Certificate of the CA by which the `registryCert` and `controllerCert` are signed | self-signed certificate generated by the operator |
| nodeSelector | string map | Labels to use for selecting Nodes on which PMEM-CSI driver should run. | `{ "storage": "pmem" }`|
| pmemPercentage | integer | Percentage of PMEM space to be used by the driver on each node. This is only valid for a driver deployed in `lvm` mode. This field can be modified, but by that time the old value may have been used already. Reducing the percentage is not supported. | 100 |
| labels | string map | Additional labels for all objects created by the operator. Can be modified after the initial creation, but removed labels will not be removed from existing objects because the operator cannot know which labels it needs to remove and which it has to leave in place. |
| kubeletDir | string | Kubelet's root directory path | /var/lib/kubelet |
| maxUnavailable | int or string | maximum number of node drivers that are allowed to be down during a rolling update, given as absolute number or percentage of the total number of nodes with the driver | 1 |

<sup>1</sup> To use the same container image as default driver image
the operator pod must set with below environment variables with
appropriate values:
 - POD_NAME: Name of the operator pod. Namespace of the pod could be figured out by the operator.
 - OPERATOR_NAME: Name of the operator container. If not set, defaults to "pmem-csi-operator"

<sup>2</sup> Image versions depend on the Kubernetes release. The
operator dynamically chooses suitable image versions. Users have to
take care of that themselves when overriding the values.

<sup>3</sup> In PMEM-CSI 0.9.0, "json" output is only available for
the PMEM-CSI container. The sidecars are still producing plain text
messages. This may change in the future. Also, the migration from
formatted log messages (= printf style) to structured log messages
(message plus key/value pairs) is not complete.

<sup>4</sup> Pod level resource requirements (`nodeResources` and `controllerResources`)
are deprecated in favor of per-container resource requirements (`nodeDriverResources`, `nodeRegistrarResources`,
`controllerDriverResources` and `provisionerResources`).

**WARNING**: although all fields can be modified and changes will be
propagated to the deployed driver, not all changes are safe. In
particular, changing the `deviceMode` will not work when there are
active volumes.

### DeploymentStatus

A PMEM-CSI Deployment's `status` field is a `DeploymentStatus` object, which
carries the detailed state of the driver deployment. It is comprised of [deployment
conditions](#deployment-conditions), [driver component status](#driver-component-status),
and a `phase` field. The phase of a PMEM-CSI deployment is a high-level summary
of where the the PmemCSIDployment is in its lifecycle.

The possible `phase` values and their meaning are as below:

| Value | Meaning |
|---|---|
| empty string | A new deployment. |
| Running | The operator has determined that the driver is usable<sup>1</sup>.  |
| Failed | For some reason, the `PmemCSIDeployment` failed and cannot be progressed. The failure reason is placed in the `DeploymentStatus.Reason` field. |

<sup>1</sup> This check has not been implemented yet. Instead, the deployment goes straight to `Running` after creating sub-resources.

### Deployment Conditions

PMEM-CSI `DeploymentStatus` has an array of `conditions` through which the
PMEM-CSI Deployment has or has not passed. Below are the possible condition
types and their meanings:

| Condition type | Meaning |
|---|---|
| CertsReady | Driver certificates/secrets are available. |
| CertsVerified | Verified that the provided certificates are valid. |
| DriverDeployed | All the componentes required for the PMEM-CSI deployment have been deployed. |

### Driver component status

PMEM-CSI `DeploymentStatus` has an array of `components` of type `DriverStatus`
in which the operator records the brief driver components status. This is
useful to know if all the driver instances of a deployment are ready.
Below are the fields and their meanings of `DriverStatus`:

| Field | Meaning |
| --- | --- |
| component | Represents the driver component type; one of `Controller` or `Node`. |
| status | Represents the state of the component; one of `Ready` or `NotReady`. Component becomes `Ready` if all the instances of the driver component are running. Otherwise, `NotReady`. |
| reason | A brief message that explains why the component is in this state. |
| lastUpdateTime | Time at which the status updated. |

### Deployment Events

The PMEM-CSI operator posts events on the progress of a `PmemCSIDeployment`. If the
deployment is in the `Failed` state, then one can look into the event(s) using
`kubectl describe` on that deployment for the detailed failure reason.

### Operator metrics data

PMEM-CSI operator exposes below metrics data about active PmemCSIDeployment
custom resources and it's sub-object in addition to the
[metrics data provided by the controller-runtime](https://book-v1.book.kubebuilder.io/beyond_basics/controller_metrics.html):

Name | Type | Explanation
-----|------|------------
`pmem_csi_deployment_reconcile` | counter | Counter that gets incremented on each time a PmemCSIDeployment CR gone through a reconcile loop, labeled with the deployment name and uid.
`pmem_csi_deployment_sub_resource_created_at` | gauge | Timestamp at which a sub resource of the PmemCSIDeployment CR was created  by the operator. Labeled by resource details ("name, "namespace", "group", "version", "kind", "uid", "ownedBy").
`pmem_csi_deployment_sub_resource_updated_at` | gauge | Timestamp at which a sub resource of the PmemCSIDeployment CR was updated by the operator. Labeled by resource details ("name, "namespace", "group", "version", "kind", "uid", "ownedBy").


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
