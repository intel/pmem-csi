# Design and architecture

## Architecture and Operation

The PMEM-CSI driver can operate in two different device modes: *LVM* and
*direct*. This table contains an overview and comparison of those modes.
There is a more detailed explanation in the following paragraphs.

|                   |`LVM`                    |`direct`              |
|:--                |:--                    |:--                 |
|Main advantage     |avoids free space fragmentation<sup>1</sup>   |simpler, somewhat faster, but free space may get fragmented<sup>1</sup>   |
|What is served     |LVM logical volume     |pmem block device   |
|Region affinity<sup>2</sup>    |yes: one LVM volume group is created per region, and a volume has to be in one volume group  |yes: namespace can belong to one region only  |
|Namespace modes    |`fsdax` mode<sup>3</sup> namespaces pre-created as pools   |namespace in `fsdax` mode created directly, no need to pre-create pools   |
|Limiting space usage | can leave part of device unused during pools creation  |no limits, creates namespaces on device until runs out of space  |
| *Name* field in namespace | *Name* gets set to 'pmem-csi' to achieve own vs. foreign marking | *Name* gets set to VolumeID, without attempting own vs. foreign marking  |
|Minimum volume size| 4 MB                   | 1 GB (see also alignment adjustment below) |
|Alignment requirements |LVM creation aligns size up to next 4MB boundary  |driver aligns  size up to next alignment boundary. The default alignment step is 1 GB. Device(s) in interleaved mode will require larger minimum as size has to be at least one alignment step. The possibly bigger alignment step is calculated as interleave-set-size multiplied by 1 GB |
|Huge pages supported<sup>4</sup> | maybe| yes|

<sup>1 </sup> **Free space fragmentation** is a problem when there appears to
be enough free capacity for a new namespace, but there isn't a contiguous
region big enough to allocate it. The PMEM-CSI driver is only capable of
allocating continguous memory to a namespace and cannot de-fragment or combine
smaller blocks. For example, this could happen when you create a 63 GB
namespace, followed by a 1 GB namespace, and then delete the 63 GB namespace.
Eventhough there is 127 GB available, the driver cannot create a namespace
larger than 64 GB. 

```
---------------------------------------------------------------------
|         63 GB free         | 1GB used |         64 GB free        |
---------------------------------------------------------------------
```

<sup>2 </sup> **Region affinity** means that all parts of a provisioned file
system are physically located on device(s) that belong to same PMEM region.
This is important on multi-socket systems where media access time may vary
based on where the storage device(s) are physically attached.

<sup>3 </sup> **fsdax mode** is required for NVDIMM
namespaces. See [Persistent Memory
Programming](https://docs.pmem.io/ndctl-user-guide/ndctl-man-pages/ndctl-create-namespace) for
details. `devdax` mode is not supported. Though a
raw block volume would be useful when a filesystem isn't needed, Kubernetes
cannot handle [binding a character device to a loop device](https://github.com/kubernetes/kubernetes/blob/7c87b5fb55ca096c007c8739d4657a5a4e29fb09/pkg/volume/util/util.go#L531-L534).

<sup>4 </sup> **Huge pages supported**: ext4 and XFS filesystems are created using the options that should enable huge
 page support, as explained in section "Verifying IO Alignment" in
 ["Using Persistent Memory Devices with the Linux Device Mapper"](https://pmem.io/2018/05/15/using_persistent_memory_devices_with_the_linux_device_mapper.html).
[Testing that support by observing page faults](/test/cmd/pmem-access-hugepages/main.go) confirmed that
this worked for direct mode. It did not work for LVM mode in the QEMU virtual machines, but it cannot be
ruled out that it works elsewhere.

## LVM device mode

In Logical Volume Management (LVM) mode the PMEM-CSI driver
uses LVM for logical volume Management to avoid the risk of fragmentation. The
LVM logical volumes are served to satisfy API requests. There is one volume
group created per region, ensuring the region-affinity of served volumes.

![devicemode-lvm diagram](/docs/images/devicemodes/pmem-csi-lvm.png)

During startup, the driver scans persistent memory for regions and
namespaces, and tries to create more namespaces using all or part
(selectable via option) of the remaining available space. Later it 
arranges physical volumes provided by namespaces into LVM volume groups.

### [Namespace modes](https://docs.pmem.io/ndctl-user-guide/concepts/nvdimm-namespaces) in LVM device mode

The PMEM-CSI driver pre-creates namespaces in `fsdax` mode forming
the corresponding LVM volume group. The amount of space to be
used is determined using the option `-pmemPercentage` given to `pmem-csi-driver`.
This options specifies an integer presenting limit as percentage.
The default value is `100`.

### Using limited amount of total space in LVM device mode

The PMEM-CSI driver can leave space on devices for others, and
recognize "own" namespaces. Leaving space for others can be achieved
by specifying lower-than-100 value to `-pmemPercentage` option.
The distinction "own" vs. "foreign" is
implemented by setting the _Name_ field in namespace to a static
string "pmem-csi" during namespace creation. When adding physical
volumes to volume groups, only those physical volumes that are based on
namespaces with the name "pmem-csi" are considered.

## Direct device mode

The following diagram illustrates the operation in Direct device mode:
![devicemode-direct diagram](/docs/images/devicemodes/pmem-csi-direct.png)

In direct device mode PMEM-CSI driver allocates namespaces directly
from the storage device. This creates device space fragmentation risk,
but reduces complexity and run-time overhead by avoiding additional
device mapping layer. Direct mode also ensures the region-affinity of
served volumes, because provisioned volume can belong to one region
only.

### [Namespace modes](https://docs.pmem.io/ndctl-user-guide/concepts/nvdimm-namespaces) in direct device mode

The PMEM-CSI driver creates a namespace directly in the mode which is
asked by volume creation request, thus bypassing the complexity of
pre-allocated pools that are used in LVM device mode.

### Using limited amount of total space in direct device mode

In direct device mode, the driver does not attempt to limit space
use. It also does not mark "own" namespaces. The _Name_ field of a
namespace gets value of the VolumeID.

## Kata Containers support

[Kata Containers](https://katacontainers.io) runs applications inside a
virtual machine. This poses a problem for App Direct mode, because
access to the filesystem prepared by PMEM-CSI is provided inside the
virtual machine by the 9p or virtio-fs filesystems. Both do not
support App Direct mode:
- 9p does not support `mmap` at all.
- virtio-fs only supports it when not using `MAP_SYNC`, i.e. without dax
  semantic.

This gets solved as follows:
- PMEM-CSI creates a volume as usual, either in direct mode or LVM mode.
- Inside that volume it sets up an ext4 or xfs filesystem.
- Inside that filesystem it creates a `pmem-csi-vm.img` file that contains
  partition tables, dax metadata and a partition that takes up most of the
  space available in the volume.
- That partition is bound to a `/dev/loop` device and the formatted
  with the requested filesystem type for the volume.
- When an application needs access to the volume, PMEM-CSI mounts
  that `/dev/loop` device.
- An application not running under Kata Containers then uses
  that filesystem normally *but* due to limitations in the Linux
  kernel, mounting might have to be done without `-odax` and thus
  App Direct access does not work.
- When the Kata Containers runtime is asked to provide access to that
  filesystem, it will instead pass the underlying `pmem-csi-vm.img`
  file into QEMU as a [nvdimm
  device](https://github.com/qemu/qemu/blob/master/docs/nvdimm.txt)
  and inside the VM mount the `/dev/pmem0p1` partition that the
  Linux kernel sets up based on the dax meta data that was placed in the
  file by PMEM-CSI. Inside the VM, the App Direct semantic is fully
  supported.

Such volumes can be used with full dax semantic *only* inside Kata
Containers. They are still usable with other runtimes, just not
with dax semantic. Because of that and the additional space overhead,
Kata Containers support has to be enabled explicitly via a [storage
class parameter and Kata Containers must be set up
appropriately](install.md#kata-containers-support).

## Dynamic provisioning of local volumes

Traditionally, Kubernetes expects that a driver deployment has a
central component, usually implemented with the `external-provisioner`
and a custom CSI driver component which implements volume creation.
That central component is hard to implement for a CSI driver that
creates volumes locally on a node.

PMEM-CSI solves this problem by deploying `external-provisioner`
alongside each node driver and enabling ["distributed
provisioning"](https://github.com/kubernetes-csi/external-provisioner/tree/v2.1.0#deployment-on-each-node):
- For volumes with storage classes that use late binding (aka "wait
  for first consumer"), a volume is tentatively assigned to a node
  before creating it, in which case the `external-provisioner` running
  on that node can tell that it is responsible for provisioning.
- The scheduler extensions help the scheduler with picking nodes where
  volumes can be created. Without them, the risk of choosing nodes
  without PMEM may be too high and manual pod scheduling may be needed
  to avoid long delays when starting pods. In the future with
  Kubernetes >= 1.21, [storage capacity
  tracking](https://kubernetes.io/docs/concepts/storage/storage-capacity/)
  will be another solution for that problem.
- For volumes with storage classes that use immediate binding, the
  different `external-provisioner` instances compete with each for
  ownership of the volume by setting the "selected node"
  annotation. Delays are used to avoid the thundering herd problem.
  Once a node has been selected, provisioning continues as with late
  binding. This is less efficient and therefore "late binding" is the
  recommended binding mode. The advantage is that this mode does not
  depend on scheduler extensions to put pods onto nodes with PMEM
  because once a volume has been created, the pod will automatically
  run on the node of the volume. The downside is that a volume might
  have been created on a node which has insufficient RAM and CPU
  resources for a pod.

PMEM-CSI also has a central component which implements the [scheduler
extender](#scheduler-extender) webhook. That component needs to know
on which nodes the PMEM-CSI driver is running and how much capacity is
available there. This information is retrieved by dynamically
discovering PMEM-CSI pods and connecting to their [metrics
endpoint](/docs/install.md#metrics-support).

## Communication between components

The following diagram illustrates the communication channels between driver components:
![communication diagram](/docs/images/communication/pmem-csi-communication-diagram.png)

## Security

The data exposed via the [metrics
endpoint](/docs/install.md#metrics-support) is not considered
confidential and therefore offered without access control via
HTTP. This also simplifies scraping that data with tools like
Prometheus.

The communication between Kubernetes and the scheduler extender
webhook is protected by TLS because this is encouraged and supported
by Kubernetes. But as the webhook only exposes information that is
already available, it accepts all incoming connection without
checking the client certificate.

The [`test/setup-ca.sh`](/test/setup-ca.sh)
script shows how to generate self-signed certificates. The test cluster is set
up using certificates created by that script, with secrets prepared by
[`test/setup-deployment.sh`](/test/setup-deployment.sh) before
deploying the driver using the provided [deployment files](/deploy/).

Beware that these are just examples. Administrators of a cluster must
ensure that they choose key lengths and algorithms of sufficient
strength for their purposes and manage certificate distribution.

A production deployment can improve upon that by using some other key
delivery mechanism, like for example
[Vault](https://www.vaultproject.io/).

## Volume Persistency

In a typical CSI deployment, volumes are provided by a storage backend
that is independent of a particular node. When a node goes offline,
the volume can be mounted elsewhere. But PMEM volumes are *local* to
node and thus can only be used on the node where they were
created. This means the applications using PMEM volume cannot freely
move between nodes. This limitation needs to be considered when
designing and deploying applications that are to use *local storage*.

These are the volume persistency models considered for implementation
in PMEM-CSI to serve different application use cases:

* **Persistent volumes**  
A volume gets created independently of the application, on some node
where there is enough free space. Applications using such a volume are
then forced to run on that node and cannot run when the node is
down. Data is retained until the volume gets deleted.

* **Ephemeral volumes**  
Each time an application starts to run on a node, a new volume is
created for it on that node. When the application stops, the volume is
deleted. The volume cannot be shared with other applications. Data on
this volume is retained only while the application runs.

Volume | Kubernetes | PMEM-CSI | Limitations
--- | --- | --- | ---
Persistent | supported | supported | topology aware scheduling<sup>1</sup>
Ephemeral | supported<sup>2</sup> | supported | resource constraints<sup>3</sup>

<sup>1 </sup>[Topology aware
scheduling](https://github.com/kubernetes/enhancements/issues/490)
ensures that an application runs on a node where the volume was
created. For CSI-based drivers like PMEM-CSI, Kubernetes >= 1.13 is
needed. On older Kubernetes releases, pods must be scheduled manually
onto the right node(s).

<sup>2 </sup> [CSI ephemeral volumes](https://kubernetes.io/docs/concepts/storage/volumes/#csi-ephemeral-volumes)
feature support is alpha in Kubernetes v1.15, and beta in v1.16.

<sup>3 </sup>The upstream design for CSI ephemeral volumes does not
take [resource
constraints](https://github.com/kubernetes/enhancements/pull/716#discussion_r250536632)
into account. If an application gets scheduled onto a node and then
creating the ephemeral volume on that node fails, the application on
the node cannot start until resources become available. This will be
solved with [generic ephemeral
volumes](https://kubernetes.io/docs/concepts/storage/ephemeral-volumes/#generic-ephemeral-volumes)
which are an alpha feature in Kubernetes 1.19 and supported by
PMEM-CSI because they use the normal volume provisioning process.

See [volume parameters](install.md#volume-parameters) for configuration information.

## Capacity-aware pod scheduling

PMEM-CSI implements the CSI `GetCapacity` call, but Kubernetes
currently doesn't call that and schedules pods onto nodes without
being aware of available storage capacity on the nodes. The effect is
that pods using volumes with late binding may get tentatively assigned
to a node and then may have to be rescheduled repeatedly until by
chance they land on a node with enough capacity. Pods using multiple
volumes with immediate binding may be unable to run permanently if
those volumes were created on different nodes.

[Storage capacity
tracking](https://kubernetes.io/docs/concepts/storage/storage-capacity/)
was added as alpha feature in Kubernetes 1.19 to enhance support for
pod scheduling with late binding of volumes.

Until that feature becomes generally available, PMEM-CSI provides two
components that help with pod scheduling:

### Scheduler extender

When a pod requests a special [extended
resource](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#extended-resources)
, the Kubernetes scheduler calls
a [scheduler
extender](https://github.com/kubernetes/design-proposals-archive/blob/main/scheduling/scheduler_extender.md)
provided by PMEM-CSI with a list of nodes that a pod might run
on.

The name of that special resource is `<CSI driver name>/scheduler`,
i.e. `pmem-csi.intel.com/scheduler` when the default PMEM-CSI driver
name is used. It is possible to configure one extender per PMEM-CSI
deployment because each deployment has its own unique driver name.

This extender is implemented in the PMEM-CSI controller and retrieves
metrics data from each PMEM-CSI node driver instance to filter out all
nodes which currently do not
have enough storage left for the volumes that still need to be
created. This considers inline ephemeral volumes and all unbound
volumes, regardless whether they use late binding or immediate
binding.

This special scheduling can be requested manually by adding this snippet
to one container in the pod spec:
```
containers:
- name: some-container
  ...
  resources:
    limits:
      pmem-csi.intel.com/scheduler: "1"
    requests:
      pmem-csi.intel.com/scheduler: "1"
```

This scheduler extender is optional and not necessarily installed in
all clusters that have PMEM-CSI. Don't add this extended resource
unless the scheduler extender is installed, otherwise the pod won't
start!

See our [implementation](http://github.com/intel/pmem-csi/tree/release-0.9/pkg/scheduler) of a scheduler extender.

### Pod admission webhook

Having to add the `<CSI driver name>/scheduler` extended resource manually is not
user-friendly. To simplify this, PMEM-CSI provides a [mutating
admission
webhook](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/)
which intercepts the creation of all pods. If that pod uses inline
ephemeral volumes or volumes with late binding that are provided by
PMEM-CSI, the webhook transparently adds the extended resource
request. PMEM-CSI volumes with immediate binding are ignored because
for those the normal topology support ensures that unsuitable nodes
are filtered out.

The webhook can only do that if the persistent volume claim (PVC) and
its storage class have been created already. This is normally not
required: it's okay to create the pod first, then later add the
PVC. The pod simply won't start in the meantime.

The webhook deals with this uncertainty by allowing the creation of
the pod without adding the extended resource when it lacks the
necessary information. The alternative would be to reject the pod, but
that would be a change of behavior of the cluster that may affect also pods
that don't use PMEM-CSI at all.

Users must take care to create PVCs first, then the pods if they want
to use the webhook. In practice, that is often already done because it
is more natural, so it is not a big limitation.

## PMEM-CSI Operator

PMEM-CSI operator facilitates deploying and managing the [PMEM-CSI driver](https://github.com/intel/pmem-csi)
on a Kubernetes cluster. This operator is based on the CoreOS [operator-sdk](https://github.com/operator-framework/operator-sdk)
tools and APIs.

The driver deployment is controlled by a cluster-scoped [custom resource](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/)
named [`PmemCSIDeployment`](./install.md#pmem-csi-deployment-crd) in the
`pmem-csi.intel.com/v1beta1` API group. The operator runs inside the cluster
and listens for deployment changes. It makes sure that the required Kubernetes
objects are created for a driver deployment. Refer to the PmemCSIDeployment
CRD for details.
