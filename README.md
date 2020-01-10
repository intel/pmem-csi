- [PMEM-CSI for Kubernetes](#pmem-csi-for-kubernetes)
    - [About](#about)
    - [Design](#design)
        - [Architecture and Operation](#architecture-and-operation)
        - [LVM device mode](#lvm-device-mode)
        - [Direct device mode](#direct-device-mode)
        - [Driver modes](#driver-modes)
        - [Driver Components](#driver-components)
        - [Communication between components](#communication-between-components)
        - [Security](#security)
        - [Volume Persistency](#volume-persistency)
    - [Prerequisites](#prerequisites)
        - [Software required](#software-required)
        - [Hardware required](#hardware-required)
        - [Persistent memory pre-provisioning](#persistent-memory-pre-provisioning)
    - [Supported Kubernetes versions](#supported-kubernetes-versions)
    - [Setup](#setup)
        - [Get source code](#get-source-code)
        - [Run PMEM-CSI on Kubernetes](#run-pmem-csi-on-kubernetes)
    - [Automated testing](#automated-testing)
        - [Unit testing and code quality](#unit-testing-and-code-quality)
        - [QEMU and Kubernetes](#qemu-and-kubernetes)
        - [Starting and stopping a test cluster](#starting-and-stopping-a-test-cluster)
        - [Running commands on test cluster nodes over ssh](#running-commands-on-test-cluster-nodes-over-ssh)
        - [Configuration options](#configuration-options)
        - [Running E2E tests](#running-e2e-tests)
    - [Application examples](#application-examples)
    - [Communication and contribution](#communication-and-contribution)

<!-- based on template now, remaining parts marked as FILL TEMPLATE:  -->

PMEM-CSI for Kubernetes
=======================


## About

---
 *Note: This is Alpha code and not production ready.*
---

Intel PMEM-CSI is a storage driver for container orchestrators like
Kubernetes. It makes local persistent memory
([PMEM](https://pmem.io/)) available as a filesystem volume to
container applications.

It can currently utilize non-volatile memory
devices that can be controlled via the [libndctl utility
library](https://github.com/pmem/ndctl). In this readme, we use
*persistent memory* to refer to a non-volatile dual in-line memory
module (NVDIMM).

The PMEM-CSI driver follows the [CSI
specification](https://github.com/container-storage-interface/spec) by
listening for API requests and provisioning volumes accordingly.


## Design

### Architecture and Operation

The PMEM-CSI driver can operate in two different device modes: *LVM* and *direct*. This table contains an overview and comparison of those modes. There is more detailed modes explanation down in following paragraphs.

|                   |LVM                    |direct              |
|:--                |:--                    |:--                 |
|Main advantage     |avoids free space fragmentation<sup>1</sup>   |simpler, somewhat faster, but free space may get fragmented<sup>1</sup>   |
|What is served     |LVM logical volume     |pmem block device   |
|Region affinity<sup>2</sup>    |yes: one LVM volume group is created per region, and a volume has to be in one volume group  |yes: namespace can belong to one region only  |
|Startup            |two extra stages: pmem-ns-init (creates namespaces), vgm (creates volume groups)   |no extra steps at startup |
|Namespace modes    |*fsdax, sector* mode<sup>3</sup> namespaces pre-created as pools   |namespace in required mode created directly, no need to pre-create pools   |
|Limiting space usage | can leave part of device unused during pools creation  |no limits, creates namespaces on device until runs out of space  |
| *Name* field in namespace | *Name* gets set to 'pmem-csi' to achieve own vs. foreign marking | *Name* gets set to VolumeID, without attempting own vs. foreign marking  |
|Minimum volume size| 4 MB                   | 1 GB (see also alignment adjustment below) |
|Alignment requirements |LVM creation aligns size up to next 4MB boundary  |driver aligns  size up to next alignment boundary. The default alignment step is 1 GB. Device(s) in interleaved mode will require larger minimum as size has to be at least one alignment step. The possibly bigger alignment step is calculated as interleave-set-size multiplied by 1 GB |

<sup>1 </sup> *Fragmented free space* state may develop via series of creation and deletion operations where the driver is no longer able to allocate a new namespace contiguously, although the free capacity (i.e. sum of available free section sizes) indicates the opposite. The PMEM-CSI driver is not capable of de-fragmenting the space or create allocation using combined smaller blocks. 
A simplified example: This state develops after steps: create 63 GB namespace, create 1 GB namespace, delete 63 GB namespace. The free capacity is 127 GB, but the driver fails to create a namespace bigger than 64 GB. 

```
---------------------------------------------------------------------
|         63 GB free         | 1GB used |         64 GB free        |
---------------------------------------------------------------------
```

<sup>2 </sup> *Region affinity* means that all parts of a provisioned file system are physically located on device(s) that belong to same PMEM region. This is important on multi-socket systems where media access time may vary based on where the storage device(s) are physically attached.

<sup>3 </sup> *fsdax, sector* modes refer to the modes of NVDIMM
namespaces. See [Persistent Memory
Programming](https://pmem.io/ndctl/ndctl-create-namespace.html) for
details. The *devdax* mode is not supported. It might make sense for a
raw block volume when the application does not need a filesystem. But
the *devdax* mode creates a character device, something that
Kubernetes does not expect from a storage driver and cannot handle
when it [binds the device to a loop
device](https://github.com/kubernetes/kubernetes/blob/7c87b5fb55ca096c007c8739d4657a5a4e29fb09/pkg/volume/util/util.go#L531-L534),

### LVM device mode

The following diagram illustrates the operation in LVM device mode:
![devicemode-lvm diagram](/docs/images/devicemodes/pmem-csi-lvm.png)

In LVM device mode PMEM-CSI driver uses LVM for logical volumes
Management to avoid the risk of fragmentation. The LVM logical volumes
are served to satisfy API requests. There is one volume group created
per region, ensuring the region-affinity of served volumes.

The driver consists of three separate binaries that form two
initialization stages and a third API-serving stage.

During startup, the driver scans persistent memory for regions and
namespaces, and tries to create more namespaces using all or part
(selectable via option) of the remaining available space. This first
stage is performed by a separate entity _pmem-ns-init_.

The second stage of initialization arranges physical volumes provided
by namespaces into LVM volume groups. This is performed by a separate
binary _pmem-vgm_.

After two initialization stages, the third binary _pmem-csi-driver_
starts serving CSI API requests.

#### Namespace modes in LVM device mode

The PMEM-CSI driver can pre-create namespaces in two modes, forming
corresponding LVM volume groups, to serve volumes based on `fsdax` or
`sector` (alias `safe`) mode namespaces. The amount of space to be
used is determined using two options `-useforfsdax` and
`-useforsector` given to _pmem-ns-init_. These options specify an
integer presenting limit as percentage, which is applied separately in
each region. The default values are `useforfsdax=100` and
`useforsector=0`. A CSI request for volume can specify the namespace
mode using the driver-specific argument `nsmode` which has a value of
either "fsdax" (default) or "sector". A volume provisioned in `fsdax`
mode will have the `dax` option added to mount options.

#### Using limited amount of total space in LVM device mode

The PMEM-CSI driver can leave space on devices for others, and
recognize "own" namespaces. Leaving space for others can be achieved
by specifying lower-than-100 values to `-useforfsdax` and/or
`-useforsector` options. The distinction "own" vs. "foreign" is
implemented by setting the _Name_ field in namespace to a static
string "pmem-csi" during namespace creation. When adding physical
volumes to volume groups, only those physical volumes that are based on
namespaces with the name "pmem-csi" are considered.

### Direct device mode

The following diagram illustrates the operation in Direct device mode:
![devicemode-direct diagram](/docs/images/devicemodes/pmem-csi-direct.png)

In direct device mode PMEM-CSI driver allocates namespaces directly
from the storage device. This creates device space fragmentation risk,
but reduces complexity and run-time overhead by avoiding additional
device mapping layer. Direct mode also ensures the region-affinity of
served volumes, because provisioned volume can belong to one region
only.

In Direct mode, the two preparation stages used in LVM mode, are not
needed.

#### Namespace modes in direct device mode

The PMEM-CSI driver creates a namespace directly in the mode which is
asked by volume creation request, thus bypassing the complexity of
pre-allocated pools that are used in LVM device mode.

#### Using limited amount of total space in direct device mode

In direct device mode, the driver does not attempt to limit space
use. It also does not mark "own" namespaces. The _Name_ field of a
namespace gets value of the VolumeID.

### Driver modes

The PMEM-CSI driver supports running in different modes, which can be
controlled by passing one of the below options to the driver's
'_-mode_' command line option. In each mode, it starts a different set
of open source Remote Procedure Call (gRPC)
[servers](#driver-components) on given driver endpoint(s).

* **_Controller_** should run as a single instance in cluster level. When the
  driver is running in _Controller_ mode, it forwards the pmem volume
  create/delete requests to the registered node controller servers
  running on the worker node. In this mode, the driver starts the
  following gRPC servers:

    * [IdentityServer](#identity-server)
    * [NodeRegistryServer](#node-registry-server)
    * [MasterControllerServer](#master-controller-server)

* One **_Node_** instance should run on each
  worker node that has persistent memory devices installed. When the
  driver starts in such mode, it registers with the _Controller_
  driver running on a given _-registryEndpoint_. In this mode, the
  driver starts the following servers:

    * [IdentityServer](#identity-server)
    * [NodeControllerServer](#node-controller-server)
    * [NodeServer](#node-server)

### Driver Components

#### Identity Server

This gRPC server operates on a given endpoint in all driver modes and
implements the CSI [Identity
interface](https://github.com/container-storage-interface/spec/blob/master/spec.md#identity-service-rpc).

#### Node Registry Server

When the PMEM-CSI driver runs in _Controller_ mode, it starts a gRPC
server on a given endpoint(_-registryEndpoint_) and serves the
[RegistryServer](pkg/pmem-registry/pmem-registry.proto) interface. The
driver(s) running in _Node_ mode can register themselves with node
specific information such as node id,
[NodeControllerServer](#node-controller-server) endpoint, and their
available persistent memory capacity.

#### Master Controller Server

This gRPC server is started by the PMEM-CSI driver running in
_Controller_ mode and serves the
[Controller](https://github.com/container-storage-interface/spec/blob/master/spec.md#controller-service-rpc)
interface defined by the CSI specification. The server responds to
CreateVolume(), DeleteVolume(), ControllerPublishVolume(),
ControllerUnpublishVolume(), and ListVolumes() calls coming from
[external-provisioner]() and [external-attacher]() sidecars. It
forwards the publish and unpublish volume requests to the appropriate
[Node controller server](#node-controller-server) running on a worker
node that was registered with the driver.

#### Node Controller Server

This gRPC server is started by the PMEM-CSI driver running in _Node_
mode and implements the
[ControllerPublishVolume](https://github.com/container-storage-interface/spec/blob/master/spec.md#controllerpublishvolume)
and
[ControllerUnpublishVolume](https://github.com/container-storage-interface/spec/blob/master/spec.md#controllerunpublishvolume)
methods of the [Controller
service](https://github.com/container-storage-interface/spec/blob/master/spec.md#controller-service-rpc)
interface defined by the CSI specification. It serves the
ControllerPublishVolume() and ControllerUnpublish() requests coming
from the [Master controller server](#master-controller-server) and
creates/deletes persistent memory devices.

#### Node Server

This gRPC server is started by the driver running in _Node_ mode and
implements the [Node
service](https://github.com/container-storage-interface/spec/blob/master/spec.md#node-service-rpc)
interface defined in the CSI specification. It serves the
NodeStageVolume(), NodeUnstageVolume(), NodePublishVolume(), and
NodeUnpublishVolume() requests coming from the Container Orchestrator
(CO).

### Communication between components

The following diagram illustrates the communication channels between driver components:
![communication diagram](/docs/images/communication/pmem-csi-communication-diagram.png)

### Security

All PMEM-CSI specific communication [shown in above
section](#communication-channels) between Master
Controller([RegistryServer](#node-registry-server),
[MasterControllerServer](#master-controller-server)) and
NodeControllers([NodeControllerServer](#node-controller-server)) is
protected by mutual TLS. Both client and server must identify
themselves and the certificate they present must be trusted. The
common name in each certificate is used to identify the different
components. The following common names have a special meaning:

- `pmem-registry` is used by the [RegistryServer](#node-registry-server).
- `pmem-node-controller` is used by [NodeControllerServers](#node-controller-server)

The [`test/setup-ca-kubernetes.sh`](test/setup-ca-kubernetes.sh)
script shows how to generate certificates signed by Kubernetes cluster
root Certificate Authority. And the provided [deployment
files](deploy/kubernetes/pmem-csi.yaml) shows how to use the generated
certificates to setup the driver. The test cluster is setup using
certificates created by that script. The
[`test/setup-ca.sh`](test/setup-ca.sh) script also shows how to
generate self signed certificates. These are just examples,
administrators of a cluster must ensure that they choose key lengths
and algorithms of sufficient strength for their purposes and manage
certificate distribution.

A production deployment can improve upon that by using some other key
delivery mechanism, like for example
[Vault](https://www.vaultproject.io/).

<!-- FILL TEMPLATE:
* Target users and use cases
* Design decisions & tradeoffs that were made
* What is in scope and outside of scope
-->

### Volume Persistency

In a typical CSI deployment, volumes are provided by a storage backend
that is independent of a particular node. When a node goes offline,
the volume can be mounted elsewhere. But PMEM volumes are *local* to
node and thus can only be used on the node where they were
created. This means the applications using PMEM volume cannot freely
move between nodes. This limitation needs to be considered when
designing and deploying applications that are to use *local storage*.

Below are the volume persistency models considered for implementation
in PMEM-CSI to serve different application use cases:

* Persistent volumes  
A volume gets created independently of the application, on some node
where there is enough free space. Applications using such a volume are
then forced to run on that node and cannot run when the node is
down. Data is retained until the volume gets deleted.

* Ephemeral volumes  
Each time an application starts to run on a node, a new volume is
created for it on that node. When the application stops, the volume is
deleted. The volume cannot be shared with other applications. Data on
this volume is retained only while the application runs.

* Cache volumes  
Volumes are pre-created on a certain set of nodes, each with its own
local data. Applications are started on those nodes and then get to
use the volume on their node. Data persists across application
restarts. This is useful when the data is only cached information that
can be discarded and reconstructed at any time *and* the application
can reuse existing local data when restarting.

Volume | Kubernetes | PMEM-CSI | Limitations
--- | --- | --- | ---
Persistent | supported | supported | topology aware scheduling<sup>1</sup>
Ephemeral | supported<sup>2</sup> | supported | resource constraints<sup>3</sup>
Cache | supported | supported | topology aware scheduling<sup>1</sup>

<sup>1 </sup>[Topology aware
scheduling](https://github.com/kubernetes/enhancements/issues/490)
ensures that an application runs on a node where the volume was
created. For CSI-based drivers like PMEM-CSI, Kubernetes >= 1.13 is
needed. On older Kubernetes releases, pods must be scheduled manually
onto the right node(s).

<sup>2 </sup> [CSI ephemeral volumes](https://kubernetes.io/docs/concepts/storage/volumes/#csi-ephemeral-volumes)
feature support is alpha in Kubernetes v1.15, and beta in v1.16.

<sup>3 </sup>The upstream design for ephemeral volumes currently does
not take [resource
constraints](https://github.com/kubernetes/enhancements/pull/716#discussion_r250536632)
into account. If an application gets scheduled onto a node and then
creating the ephemeral volume on that node fails, the application on
the node cannot start until resources become available.

#### Usage on Kubernetes

Kubernetes cluster administrators can expose above mentioned persistent and cache volumes
to applications using
[`StorageClass
Parameters`](https://kubernetes.io/docs/concepts/storage/storage-classes/#parameters). An
optional `persistencyModel` parameter differentiates how the
provisioned volume can be used.

* if no `persistencyModel` parameter specified in `StorageClass` then
  it is treated as normal Kubernetes persistent volume. In this case
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
`ReadWriteMany` in its `accessModes` list. Check with provided [cache
StorageClass](deploy/common/pmem-storageclass-cache.yaml)
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
Check with the provided [cache
application](deploy/common/pmem-app-cache.yaml) example.

**WARNING**: late binding (`volumeBindingMode:WaitForFirstConsume`) has some caveats:
* Kubernetes does not consider available PMEM capacity on a node while scheduling the application.
  As a result, Kubernetes might select a node that does not have enough free PMEM space. In this case,
  volume creation fails and the pod is stuck until enough free space becomes
  available.
* A node is only chosen the first time a pod starts. After that it will always restart
  on that node, because that is where the persistent volume was created.

Volume requests embedded in Pod spec are provisioned as ephemeral volumes. The volume request could use below fields as [`volumeAttributes`](https://kubernetes.io/docs/concepts/storage/volumes/#csi):

|key|meaning|optional|values|
|---|-------|--------|-------------|
|`size`|Size of the requested ephemeral volume|No||
|`nsmode`|PMEM namespace mode of requested<br> ephemeral volume|Yes|`fsdax` (default),<br> `sector`|
|`eraseAfter`|Clear all data after use and before<br> deleting the volume|Yes|`true` (default),<br> `false`|

Check with provided [example application](deploy/kubernetes-1.15/pmem-app-ephemeral.yaml) for
ephemeral volume usage.
## Prerequisites

### Software required

The recommended mimimum Linux kernel version for running the PMEM-CSI driver is 4.15. See [Persistent Memory Programming](https://pmem.io/2018/05/15/using_persistent_memory_devices_with_the_linux_device_mapper.html) for more details about supported kernel versions.

### Hardware required

Persistent memory device(s) are required for operation. However, some
development and testing can be done using QEMU-emulated persistent
memory devices. See the ["QEMU and Kubernetes"](#qemu-and-kubernetes)
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
```sh
# ipmctl create -goal PersistentMemoryType=AppDirectNotInterleaved
```

Alternatively, multiple NVDIMMs can be combined to form an interleaved set.
This causes the data to be striped over multiple NVDIMM devices
for improved read/write performance and allowing one region (also, PMEM-CSI volume)
to be larger than single NVDIMM.

Example of creating regions in interleaved mode, using all NVDIMMs:
```sh
# ipmctl create -goal PersistentMemoryType=AppDirect
```

When running inside virtual machines, each virtual machine typically
already gets access to one region and `ipmctl` is not needed inside
the virtual machine. Instead, that region must be made available for
use with PMEM-CSI because when the virtual machine comes up for the
first time, the entire region is already allocated for use as a single
block device:
``` sh
# ndctl list -RN
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
# ls -l /dev/pmem*
brw-rw---- 1 root disk 259, 0 Jun  4 16:41 /dev/pmem0
```

Labels must be initialized in such a region, which must be performed
once after the first boot:
``` sh
# ndctl disable-region region0
disabled 1 region
# ndctl init-labels nmem0
initialized 1 nmem
# ndctl enable-region region0
enabled 1 region
# ndctl list -RN
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
# ls -l /dev/pmem*
ls: cannot access '/dev/pmem*': No such file or directory
```

## Supported Kubernetes versions

PMEM-CSI implements the CSI specification version 1.x, which is only
supported by Kubernetes versions >= v1.13. The following table
summarizes the status of support for PMEM-CSI on different Kubernetes
versions:

| Kubernetes version | Required alpha feature gates   | Support status
|--------------------|--------------------------------|----------------
| 1.13               | CSINodeInfo, CSIDriverRegistry,<br>CSIBlockVolume</br>| unsupported <sup>1</sup>
| 1.14               |                                |
| 1.15               | CSIInlineVolume                |
| 1.16               |                                |

<sup>1</sup> Several relevant features are only available in alpha
quality in Kubernetes 1.13 and the combination of skip attach and
block volumes is completely broken, with [the
fix](https://github.com/kubernetes/kubernetes/pull/79920) only being
available in later versions. The external-provisioner v1.0.1 for
Kubernetes 1.13 lacks the `--strict-topology` flag and therefore late
binding is unreliable. It's also a release that is not supported
officially by upstream anymore.


## Setup

### Get source code

PMEM-CSI uses Go modules and thus can be checked out and (if that should be desired)
built anywhere in the filesystem. Pre-built container images are available and thus
users don't need to build from source, but they will still need some additional files.
To get the source code, use:

```
git clone https://github.com/intel/pmem-csi
```

### Run PMEM-CSI on Kubernetes

This section assumes that a Kubernetes cluster is already available
with at least one node that has persistent memory device(s). For development or
testing, it is also possible to use a cluster that runs on QEMU virtual
machines, see the ["QEMU and Kubernetes"](#qemu-and-kubernetes) section below.

- **Make sure that the alpha feature gates CSINodeInfo and CSIDriverRegistry are enabled**

The method to configure alpha feature gates may vary, depending on the Kubernetes deployment.
It may not be necessary anymore when the feature has reached beta state, which depends
on the Kubernetes version.

- **Label the cluster nodes that provide persistent memory device(s)**

```sh
    $ kubectl label node <your node> storage=pmem
```

- **Set up certificates**

Certificates are required as explained in [Security](#security).
If you are not using the test cluster described in
[Starting and stopping a test cluster](#starting-and-stopping-a-test-cluster)
where certificates are created automatically, you must set up certificates manually.
This can be done by running the `./test/setup-ca-kubernetes.sh` script for your cluster.
This script requires "cfssl" tools which can be downloaded.
These are the steps for manual set-up of certificates:

- Download cfssl tools

```sh
   $ curl -L https://pkg.cfssl.org/R1.2/cfssl_linux-amd64 -o _work/bin/cfssl --create-dirs
   $ curl -L https://pkg.cfssl.org/R1.2/cfssljson_linux-amd64 -o _work/bin/cfssljson --create-dirs
   $ chmod a+x _work/bin/cfssl _work/bin/cfssljson
```

- Run certificates set-up script

```sh
   $ KUBCONFIG="<<your cluster kubeconfig path>> PATH="$PATH:$PWD/_work/bin" ./test/setup-ca-kubernetes.sh
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

For example, to deploy for production with LVM device mode onto Kubernetes 1.14, use:

```sh
    $ kubectl create -f deploy/kubernetes-1.14/pmem-csi-lvm.yaml
```

These variants were generated with
[`kustomize`](https://github.com/kubernetes-sigs/kustomize).
`kubectl` >= 1.14 includes some support for that. The sub-directories
of `deploy/kubernetes-<kubernetes version>` can be used as bases
for `kubectl kustomize`. For example:

   - Change namespace:
     ```
     $ mkdir -p my-pmem-csi-deployment
     $ cat >my-pmem-csi-deployment/kustomization.yaml <<EOF
     namespace: pmem-csi
     bases:
       - ../deploy/kubernetes-1.14/lvm
     EOF
     $ kubectl create namespace pmem-csi
     $ kubectl create --kustomize my-pmem-csi-deployment
     ```

   - Configure how much PMEM is used by PMEM-CSI for LVM
     (see [Namespace modes in LVM device mode](#namespace-modes-in-lvm-device-mode)):
     ```
     $ mkdir -p my-pmem-csi-deployment
     $ cat >my-pmem-csi-deployment/kustomization.yaml <<EOF
     bases:
       - ../deploy/kubernetes-1.14/lvm
     patchesJson6902:
       - target:
           group: apps
           version: v1
           kind: DaemonSet
           name: pmem-csi-node
         path: lvm-parameters-patch.yaml
     EOF
     $ cat >my-pmem-csi-deployment/lvm-parameters-patch.yaml <<EOF
     # pmem-ns-init is in the init container #0. Append arguments at the end.
     - op: add
       path: /spec/template/spec/initContainers/0/args/-
       value: "--useforfsdax=90"
     - op: add
       path: /spec/template/spec/initContainers/0/args/-
       value: "--useforsector=0"
     EOF
     $ kubectl create --kustomize my-pmem-csi-deployment
     ```

- **Wait until all pods reach 'Running' status**

```sh
    $ kubectl get pods
    NAME                    READY   STATUS    RESTARTS   AGE
    pmem-csi-node-8kmxf     2/2     Running   0          3m15s
    pmem-csi-node-bvx7m     2/2     Running   0          3m15s
    pmem-csi-controller-0   2/2     Running   0          3m15s
    pmem-csi-node-fbmpg     2/2     Running   0          3m15s
```

- **Verify that the node labels have been configured correctly**

```sh
    $ kubectl get nodes --show-labels
```

The command output must indicate that every node with PMEM has these two labels:
```
pmem-csi.intel.com/node=<NODE-NAME>,storage=pmem
```

If **storage=pmem** is missing, label manually as described above. If
**pmem-csi.intel.com/node** is missing, then double-check that the
alpha feature gates are enabled, that the CSI driver is running on the node,
and that the driver's log output doesn't contain errors.

- **Define two storage classes using the driver**

```sh
    $ kubectl create -f deploy/kubernetes-<kubernetes version>/pmem-storageclass-ext4.yaml
    $ kubectl create -f deploy/kubernetes-<kubernetes version>/pmem-storageclass-xfs.yaml
```

- **Provision two pmem-csi volumes**

```sh
    $ kubectl create -f deploy/kubernetes-<kubernetes version>/pmem-pvc.yaml
```

- **Verify two Persistent Volume Claims have 'Bound' status**

```sh
    $ kubectl get pvc
    NAME                STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS       AGE
    pmem-csi-pvc-ext4   Bound    pvc-f70f7b36-6b36-11e9-bf09-deadbeef0100   4Gi        RWO            pmem-csi-sc-ext4   16s
    pmem-csi-pvc-xfs    Bound    pvc-f7101fd2-6b36-11e9-bf09-deadbeef0100   4Gi        RWO            pmem-csi-sc-xfs    16s
```

- **Start two applications requesting one provisioned volume each**

```sh
    $ kubectl create -f deploy/kubernetes-<kubernetes version>/pmem-app.yaml
```

These applications use **storage: pmem** in the <i>nodeSelector</i>
list to ensure scheduling to a node supporting pmem device, and each requests a mount of a volume,
one with ext4-format and another with xfs-format file system.

- **Verify two application pods reach 'Running' status**

```sh
    $ kubectl get po my-csi-app-1 my-csi-app-2
    NAME           READY   STATUS    RESTARTS   AGE
    my-csi-app-1   1/1     Running   0          6m5s
    NAME           READY   STATUS    RESTARTS   AGE
    my-csi-app-2   1/1     Running   0          6m1s
```

- **Check that applications have a pmem volume mounted with added dax option**

```sh
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

#### Note about using the sector mode

PMEM-CSI can create a namespace in the *sector* (alias safe) mode instead of default *fsdax* mode. See [Persistent Memory Programming](https://pmem.io/ndctl/ndctl-create-namespace.html) for more details about namespace modes. The main difference in PMEM-CSI context is that a sector-mode volume will not get 'dax' mount option. The deployment examples do not describe sector mode to keep the amount of combinations smaller. Here are the changes to be made to deploy volumes in sector mode:

- add `nsmode: "sector"` line in parameters section in the storageclass definition file pmem-storageclass-XYZ.yaml:
```
parameters:
  csi.storage.k8s.io/fstype: ext4
  eraseafter: "true"
  nsmode: "sector"                   <-- add this
```

* Only if using LVM device mode: Modify pmem-ns-init options to create sector-mode pools in addition to fsdax-mode pools. Add `-useforfsdax` and `-useforsector` options to pmem-ns-init arguments in pmem-csi-lvm.yaml (select the percentage values that fit your needs):
```
       initContainers:
       - args:
         - -v=3
         - -useforfsdax=60                   <-- add this
         - -useforsector=40                  <-- add this
         command:
         - /go/bin/pmem-ns-init
```

  This change can be made either by copying and editing a deployment
  yaml file or on-the-fly with `kustomize` as explained in [Run
  PMEM-CSI on Kubernetes](#run-pmem-csi-on-kubernetes).


#### Note about raw block volumes

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
deploy/common/pmem-pvc-block-volume.yaml) and
[application](deploy/common/pmem-app-block-volume.yaml) for usage reference.

That example demonstrates how to handle some details:
- `mkfs.ext4` needs `-b 4096` to produce volumes that support dax;
  without it, the automatic block size detection may end up choosing
  an unsuitable value depending on the volume size.
- [Kubernetes bug #85624](https://github.com/kubernetes/kubernetes/issues/85624)
  must be worked around to format and mount the raw block device.

<!-- FILL TEMPLATE:

  ### How to extend the plugin

You can modify PMEM-CSI to support more xxx by changing the `variable` from Y to Z.


  ## Maintenance

* Known limitations
* What is supported and what isn't supported
    * Disclaimer that nothing is supported with any kind of SLA
* Example configuration for target use case
* How to upgrade
* Upgrade cadence


  ## Troubleshooting

* If you see this error, then enter this command `blah`.
-->


## Automated testing

### Unit testing and code quality

Use the `make test` command.

### QEMU and Kubernetes

E2E testing relies on a cluster running inside multiple QEMU virtual
machines deployed by [GoVM](https://github.com/govm-project/govm). The
same cluster can also be used interactively when real hardware is not
available.

E2E testing is known to work on a Linux development host system. The user
must be allowed to use Docker.

KVM must be enabled. Usually this is the case when `/dev/kvm` exists.
The current user does not need the privileges to use KVM and QEMU
doesn't have to be installed because GoVM will run QEMU inside a
container with root privileges.

Note that cloud providers often don't offer KVM support on their
regular machines. Search for "nested virtualization" for your provider
to determine whether and how it supports KVM.

Nested virtualization is also needed when using Kata Containers inside
the cluster. On Intel-based machines it can be enabled by loading the
`kvm_intel` module with `nested=1` (see
https://wiki.archlinux.org/index.php/KVM#Nested_virtualization). At
this time, Kata Containers up to and including 1.9.1 is [not
compatible with
PMEM-CSI](https://github.com/intel/pmem-csi/issues/303) because
volumes are not passed in as PMEM, but Kata Containers [can be
installed](https://github.com/kata-containers/packaging/tree/master/kata-deploy#kubernetes-quick-start)
and used for applications that are not using PMEM.

The `clear-cloud` image is downloaded automatically. By default,
four different virtual machines are prepared. Each image is pre-configured
with its own hostname and with network.

The images will contain the latest
[Clear Linux OS](https://clearlinux.org/) and have the Kubernetes
version supported by Clear Linux installed.

PMEM-CSI images must have been created and published in some Docker
registry, as described earlier in [build PMEM-CSI](#build-pmem-csi).
In addition, that registry must be accessible from inside the
cluster. That works for the default (a local registry in the build
host) but may require setting additional [configuration
options](#configuration-options) for other scenarios.

### Starting and stopping a test cluster

`make start` will bring up a Kubernetes test cluster inside four QEMU
virtual machines.
The first node is the Kubernetes master without
persistent memory.
The other three nodes are worker nodes with one emulated 32GB NVDIMM each.
After the cluster has been formed, `make start` adds `storage=pmem` label
to the worker nodes and deploys the PMEM-CSI driver.
Once `make start` completes, the cluster is ready for interactive use via
`kubectl` inside the virtual machine. Alternatively, you can also
set `KUBECONFIG` as shown at the end of the `make start` output
and use `kubectl` binary on the host running VMs.

When the cluster is already running, `make start` will re-deploy the
PMEM-CSI driver without recreating the virtual machines. `kubectl
apply` is used for this, which may limit the kind of changes that can
be made on-the-fly.

Use `make stop` to stop and remove the virtual machines.

`make restart` can be used to cleanly reboot all virtual
machines. This is useful during development after a `make push-images`
to ensure that the cluster runs those rebuilt images.

### Running commands on test cluster nodes over ssh

`make start` generates ssh wrapper scripts `_work/pmem-govm/ssh.N` for each
test cluster node which are handy for running a single command or to
start an interactive shell. Examples:

`_work/pmem-govm/ssh.0 kubectl get pods` runs a kubectl command on
the master node.

`_work/pmem-govm/ssh.1` starts a shell on the first worker node.

### Configuration options

Several aspects of the cluster and build setup can be configured by overriding
the settings in the [test-config.sh](test/test-config.sh) file. See
that file for a description of all options. Options can be set as
environment variables of `make start` on a case-by-case basis or
permanently by creating a file like `test/test-config.d/my-config.sh`.

Multiple different clusters can be brought up in parallel by changing
the default `pmem-govm` cluster name via the `CLUSTER` env variable.

For example, this invocation sets up a cluster using the non-default
direct device mode:

``` sh
TEST_DEVICEMODE=direct CLUSTER=clear-govm-direct make start
```

### Running E2E tests

`make test_e2e` will run [csi-test
sanity](https://github.com/kubernetes-csi/csi-test/tree/master/pkg/sanity)
tests and some [Kubernetes storage
tests](https://github.com/kubernetes/kubernetes/tree/master/test/e2e/storage/testsuites)
against the PMEM-CSI driver.

When [ginkgo](https://onsi.github.io/ginkgo/) is installed, then it
can be used to run individual tests and to control additional aspects
of the test run. For example, to run just the E2E provisioning test
(create PVC, write data in one pod, read it in another) in verbose mode:

``` sh
$ KUBECONFIG=$(pwd)/_work/pmem-govm/kube.config REPO_ROOT=$(pwd) ginkgo -v -focus=pmem-csi.*should.provision.storage.with.defaults ./test/e2e/
Nov 26 11:21:28.805: INFO: The --provider flag is not set.  Treating as a conformance test.  Some tests may not be run.
Running Suite: PMEM E2E suite
=============================
Random Seed: 1543227683 - Will randomize all specs
Will run 1 of 61 specs

Nov 26 11:21:28.812: INFO: checking config
Nov 26 11:21:28.812: INFO: >>> kubeConfig: /nvme/gopath/src/github.com/intel/pmem-csi/_work/pmem-govm/kube.config
Nov 26 11:21:28.817: INFO: Waiting up to 30m0s for all (but 0) nodes to be schedulable
...
Ran 1 of 61 Specs in 58.465 seconds
SUCCESS! -- 1 Passed | 0 Failed | 0 Pending | 60 Skipped
PASS

Ginkgo ran 1 suite in 1m3.850672246s
Test Suite Passed
```

It is also possible to run just the sanity tests until one of them fails:

``` sh
$ REPO_ROOT=`pwd` ginkgo '-focus=sanity' -failFast ./test/e2e/
...
```

## Application examples

Information about specific usages of PMEM-CSI are described in separate documents:

* Deploying a Redis cluster through the redis-operator using QEMU-emulated persistent memory devices ([examples/redis-operator.md](examples/redis-operator.md)).
* Installing Kubernetes and PMEM-CSI on Google Cloud machines. ([examples/gce.md](examples/gce.md)).

## Communication and contribution

Report a bug by [filing a new issue](https://github.com/intel/pmem-csi/issues).

Contribute by [opening a pull request](https://github.com/intel/pmem-csi/pulls).

Learn [about pull requests](https://help.github.com/articles/using-pull-requests/).

**Reporting a Potential Security Vulnerability:** If you have discovered potential security vulnerability in PMEM-CSI, please send an e-mail to secure@intel.com. For issues related to Intel Products, please visit [Intel Security Center](https://security-center.intel.com).

It is important to include the following details:

- The projects and versions affected
- Detailed description of the vulnerability
- Information on known exploits

Vulnerability information is extremely sensitive. Please encrypt all security vulnerability reports using our [PGP key](https://www.intel.com/content/www/us/en/security-center/pgp-public-key.html).

A member of the Intel Product Security Team will review your e-mail and contact you to collaborate on resolving the issue. For more information on how Intel works to resolve security issues, see: [vulnerability handling guidelines](https://www.intel.com/content/www/us/en/security-center/vulnerability-handling-guidelines.html).

<!-- FILL TEMPLATE:
Contact the development team (*TBD: slack or email?*)


  ## References

Pointers to other useful documentation.

* Video tutorial
    * Simple youtube style. Demo installation following steps in readme.
      Useful to show relevant paths. Helps with troubleshooting.
-->
