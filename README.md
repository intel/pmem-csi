-   [PMEM-CSI for Kubernetes](#pmem-csi-for-kubernetes)
    -   [About](#about)
    -   [Design](#design)
        -   [Architecture and Operation](#architecture-and-operation)
        -   [DeviceMode:LVM](#devicemodelvm)
        -   [DeviceMode:Direct](#devicemodedirect)
        -   [Driver modes](#driver-modes)
        -   [Driver Components](#driver-components)
        -   [Communication between components](#communication-between-components)
        -   [Security](#security)
        -   [Volume Persistency](#volume-persistency)
    -   [Prerequisites](#prerequisites)
        -   [Software required](#software-required)
        -   [Hardware required](#hardware-required)
    -   [Supported Kubernetes versions](#supported-kubernetes-versions)
    -   [Setup](#setup)
        -   [Get source code](#get-source-code)
        -   [Build PMEM-CSI](#build-pmem-csi)
        -   [Run PMEM-CSI on Kubernetes](#run-pmem-csi-on-kubernetes)
    -   [Automated testing](#automated-testing)
        -   [Unit testing and code quality](#unit-testing-and-code-quality)
        -   [QEMU and Kubernetes](#qemu-and-kubernetes)
        -   [Starting and stopping a test cluster](#starting-and-stopping-a-test-cluster)
        -   [Running commands on test cluster nodes over ssh](#running-commands-on-test-cluster-nodes-over-ssh)
        -   [Running E2E tests](#running-e2e-tests)
    -   [Communication and contribution](#communication-and-contribution)

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

The PMEM-CSI driver can operate in two different DeviceModes: LVM and Direct

### DeviceMode:LVM

The following diagram illustrates the operation in DeviceMode:LVM:
![devicemode-lvm diagram](/docs/images/devicemodes/pmem-csi-lvm.png)

In DeviceMode:LVM PMEM-CSI driver uses LVM for Logical Volumes
Management to avoid the risk of fragmentation. The LVM logical volumes
are served to satisfy API requests. There is one Volume Group created
per Region, ensuring the region-affinity of served volumes.

The driver consists of three separate binaries that form two
initialization stages and a third API-serving stage.

During startup, the driver scans persistent memory for regions and
namespaces, and tries to create more namespaces using all or part
(selectable via option) of the remaining available space. The
namespace size can be specified as a driver parameter and defaults to
32 GB. This first stage is performed by a separate entity
_pmem-ns-init_.

The second stage of initialization arranges physical volumes provided
by namespaces into LVM volume groups. This is performed by a separate
binary _pmem-vgm_.

After two initialization stages, the third binary _pmem-csi-driver_
starts serving CSI API requests.

#### Namespace modes in DeviceMode:LVM

The PMEM-CSI driver can pre-create Namespaces in two modes, forming
corresponding LVM Volume groups, to serve volumes based on `fsdax` or
`sector` (alias `safe`) mode Namespaces. The amount of space to be
used is determined using two options `-useforfsdax` and
`-useforsector` given to _pmem-ns-init_. These options specify an
integer presenting limit as percentage, which is applied separately in
each Region. The default values are `useforfsdax=100` and
`useforsector=0`. A CSI request for volume can specify the Namespace
mode using the driver-specific argument `nsmode` which has a value of
either "fsdax" (default) or "sector". A volume provisioned in `fsdax`
mode will have the `dax` option added to mount options.

#### Using limited amount of total space in DeviceMode:LVM

The PMEM-CSI driver can leave space on devices for others, and
recognize "own" namespaces. Leaving space for others can be achieved
by specifying lower-than-100 values to `-useforfsdax` and/or
`-useforsector` options. The distinction "own" vs. "foreign" is
implemented by setting the _Name_ field in Namespace to a static
string "pmem-csi" during Namespace creation. When adding Physical
Volumes to Volume Groups, only Physical Volumes that are based on
Namespaces with the name "pmem-csi" are considered.

### DeviceMode:Direct

The following diagram illustrates the operation in DeviceMode:Direct:
![devicemode-direct diagram](/docs/images/devicemodes/pmem-csi-direct.png)

In DeviceMode:Direct PMEM-CSI driver allocates Namespaces directly
from the storage device. This creates device space fragmentation risk,
but reduces complexity and run-time overhead by avoiding additional
device mapping layer. Direct mode also ensures the region-affinity of
served volumes, because provisioned volume can belong to one Region
only.

In Direct mode, the two preparation stages used in LVM mode, are not
needed.

#### Namespace modes in DeviceMode:Direct

The PMEM-CSI driver creates a Namespace directly in the mode which is
asked by Volume creation request, thus bypassing the complexity of
pre-allocated pools that are used in DeviceMode:LVM.

#### Using limited amount of total space in DeviceMode:Direct

In DeviceMode:Direct, the driver does not attempt to limit space
use. It also does not mark "own" namespaces. The _Name_ field of a
Namespace gets value of the VolumeID.

### Driver modes

The PMEM-CSI driver supports running in different modes, which can be
controlled by passing one of the below options to the driver's
'_-mode_' command line option. In each mode, it starts a different set
of open source Remote Procedure Call (gRPC)
[servers](#driver-components) on given driver endpoint(s).

* **_Controller_** mode is intended to be used in a multi-node cluster
  and should run as a single instance in cluster level. When the
  driver is running in _Controller_ mode, it forwards the pmem volume
  create/delete requests to the registered node controller servers
  running on the worker node. In this mode, the driver starts the
  following gRPC servers:

    * [IdentityServer](#identity-server)
    * [NodeRegistryServer](#node-registry-server)
    * [MasterControllerServer](#master-controller-server)

* **_Node_** mode is intended to be used in a multi-node cluster by
  worker nodes that have persistent memory devices installed. When the
  driver starts in this mode, it registers with the _Controller_
  driver running on a given _-registryEndpoint_. In this mode, the
  driver starts the following servers:

    * [IdentityServer](#identity-server)
    * [NodeControllerServer](#node-controller-server)
    * [NodeServer](#node-server)

* **_Unified_** mode is intended to run the driver in a single host,
  mostly for testing the driver in a non-clustered environment.

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

* Persistent Volumes  
A volume gets created independently of the application, on some node
where there is enough free space. Applications using such a volume are
then forced to run on that node and cannot run when the node is
down. Data is retained until the volume gets deleted.

* Ephemeral Volumes  
Each time an application starts to run on a node, a new volume is
created for it on that node. When the application stops, the volume is
deleted. The volume cannot be shared with other applications. Data on
this volume is retained only while the application runs.

* Cache Volumes  
Volumes are pre-created on a certain set of nodes, each with its own
local data. Applications are started on those nodes and then get to
use the volume on their node. Data persists across application
restarts. This is useful when the data is only cached information that
can be discarded and reconstructed at any time *and* the application
can reuse existing local data when restarting.

Volume | Kubernetes | PMEM-CSI | Limitations
--- | --- | --- | ---
Persistent | supported | supported | topology aware scheduling<sup>1</sup>
Ephemeral | [in design](https://github.com/kubernetes/enhancements/blob/master/keps/sig-storage/20190122-csi-inline-volumes.md#proposal) | in design | topology aware scheduling<sup>1</sup>, resource constraints<sup>2</sup>
Cache | supported | supported | topology aware scheduling<sup>1</sup>

<sup>1 </sup>[Topology aware
scheduling](https://github.com/kubernetes/enhancements/issues/490)
ensures that an application runs on a node where the volume was
created. For CSI-based drivers like PMEM-CSI, Kubernetes >= 1.13 is
needed. On older Kubernetes releases, pods must be scheduled manually
onto the right node(s).

<sup>2 </sup>The upstream design for ephemeral volumes currently does
not take [resource
constraints](https://github.com/kubernetes/enhancements/pull/716#discussion_r250536632)
into account. If an application gets scheduled onto a node and then
creating the ephemeral volume on that node fails, the application on
the node cannot start until resources become available.

#### Usage on Kubernetes

Kubernetes cluster administrators can expose above mentioned [volume
persistency types](#volume-persistency) to applications using
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
  WaitForFirstConsumer` Kubernetes first chooses a node for scheduling
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
StorageClass](deploy/kubernetes-1.13/pmem-storageclass-cache.yaml)
example. This
[diagram](/docs/images/sequence/pmem-csi-cache-sequence-diagram.png)
illustrates how a cache volume gets provisioned in Kubernetes using
PMEM-CSI driver.

**NOTE**: Cache volumes are local to node not Pod. If two Pods using
the same cache volume runs on the same node, will not get their own
local volume, instead they endup sharing the same PMEM
volume. Applications has to consider this and use available Kubernetes
mechanisms like [node
aniti-affinity](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#affinity-and-anti-affinity)
while deploying. Check with provided [cache
application](deploy/kubernetes-1.13/pmem-app-cache.yaml) example.

## Prerequisites

### Software required

Building of Docker images has been verified using Docker-ce: version 18.06.1

### Hardware required

Persistent memory device(s) are required for operation. However, some
development and testing can be done using QEMU-emulated persistent
memory devices, see [README-qemu-notes](README-qemu-notes.md) for
technical details and the ["QEMU and Kubernetes"](#qemu-and-kubernetes)
section for the commands that create such a virtual test cluster.

The driver does not create persistent memory Regions, but expects
Regions to exist when the driver starts. The utility
[ipmctl](https://github.com/intel/ipmctl) can be used to create
Regions.

## Supported Kubernetes versions

PMEM-CSI driver implements CSI specification version 1.0.0, which only
supported by Kubernetes versions >= v1.13. The driver deployment in
Kubernetes cluster has been verified on:

| Branch            | Kubernetes branch/version      | Required alpha feature gates   |
|-------------------|--------------------------------|------------------------------- |
| devel             | Kubernetes 1.13                | CSINodeInfo, CSIDriverRegistry |

## Setup

### Get source code

Use these commands:

```
mkdir -p $GOPATH/src/github.com/intel
git clone https://github.com/intel/pmem-csi $GOPATH/src/github.com/intel/pmem-csi
```

### Build PMEM-CSI

1.  Use `make build-images` to produce Docker container images.

2.  Use `make push-images` to push Docker container images to a Docker images registry. The
    default is to push to a local [Docker registry](https://docs.docker.com/registry/deploying/).

See the [Makefile](Makefile) for additional make targets and possible make variables.

### Run PMEM-CSI on Kubernetes

This section assumes that a Kubernetes cluster is already available
with at least one node that has persistent memory device(s). For development or
testing, it is also possible to use a cluster that runs on QEMU virtual
machines, see the ["QEMU and Kubernetes"](#qemu-and-kubernetes) section below.

- **Label the cluster nodes that provide persistent memory device(s)**

```sh
    $ kubectl label node <your node> storage=pmem
```

- **Deploy the driver to Kubernetes using DeviceMode:LVM**

```sh
    $ sed -e 's/192.168.8.1:5000/<your registry>/' deploy/kubernetes-<kubernetes version>/pmem-csi-lvm.yaml | kubectl create -f -
```

- **Alternatively, deploy the driver to Kubernetes using DeviceMode:Direct**

```sh
    $ sed -e 's/192.168.8.1:5000/<your registry>/' deploy/kubernetes-<kubernetes version>/pmem-csi-direct.yaml | kubectl create -f -
```

The deployment yaml file uses the registry address for the QEMU test cluster
setup (see below). When deploying on a real cluster, some registry
that can be accessed by that cluster has to be used.
If the Docker registry runs on the local development
host, then the `sed` command which replaces the Docker registry is not needed.
The `deploy` directory contains one directory or symlink for each
tested Kubernetes release. The most recent one might also work on
future, currently untested releases.

- **Define a storage class using the driver**

```sh
    $ kubectl create -f deploy/kubernetes-<kubernetes version>/pmem-storageclass.yaml
```

- **Provision a pmem-csi volume**

```sh
    $ kubectl create -f deploy/kubernetes-<kubernetes version>/pmem-pvc.yaml
```

- **Start an application requesting provisioned volume**

```sh
    $ kubectl create -f deploy/kubernetes-<kubernetes version>/pmem-app.yaml
```

The application uses **storage: pmem** in its <i>nodeSelector</i>
list to ensure that it runs on the right node.

- **Once the application pod is in 'Running' status, check that it has a pmem volume**

```sh
    $ kubectl get po my-csi-app
    NAME                        READY     STATUS    RESTARTS   AGE
    my-csi-app                  1/1       Running   0          1m
    
    $ kubectl exec my-csi-app -- df /data
    Filesystem           1K-blocks      Used Available Use% Mounted on
    /dev/ndbus0region0/7a4cc7b2-ddd2-11e8-8275-0a580af40161
                           8191416     36852   7718752   0% /data
```

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

**Note:** Testing code is not completed yet. Currently it runs some passes using `gofmt, go vet`.

### QEMU and Kubernetes

E2E testing relies on a cluster running inside multiple QEMU virtual
machines. The same cluster can also be used interactively when
real hardware is not available.

This is known to work on a Linux development host system.
The `qemu-system-x86_64` binary must be installed, either from
[upstream QEMU](https://www.qemu.org/) or the Linux distribution.
The user must be able to run commands as root via `sudo`.
For networking, the `ip` tool from the `iproute2` package must be
installed. The following command must be run once after booting the
host machine and before starting the virtual machine:

    test/runqemu-ifup 4

This configures four tap devices for use by the current user. At the
moment, the test setup uses:

- `pmemtap0/1/2/3`
- `pmembr0`
- 192.168.8.1 for the build host side of the bridge interfaces
- 192.168.8.2/4/6/8 for the virtual machines
- the same DNS server for the virtual machines as on the development
  host

It is possible to configure this by creating one or more files ending
in `.sh` (for shell) in the directory `test/test-config.d` and setting
shell variables in those files. For all supported options, see
[test-config.sh](test/test-config.sh).

To undo the configuration changes made by `test/runqemu-ifup` when
the tap device is no longer needed, run:

    test/runqemu-ifdown

KVM must be enabled and the user must be allowed to use it. Usually this
is done by adding the user to the `kvm` group. The
["Install QEMU-KVM"](https://clearlinux.org/documentation/clear-linux/get-started/virtual-machine-install/kvm)
section in the Clear Linux documentation contains further information
about enabling KVM and installing QEMU.

To ensure that QEMU and KVM are working, run this:

    make _work/clear-kvm-original.img _work/start-clear-kvm _work/OVMF.fd
    cp _work/clear-kvm-original.img _work/clear-kvm-test.img
    _work/start-clear-kvm _work/clear-kvm-test.img

The result should be a login prompt like this:

    [    0.049839] kvm: no hardware support
    
    clr-c3f99095d2934d76a8e26d2f6d51cb91 login: 

The message about missing KVM hardware support comes from inside the
virtual machine and indicates that nested KVM is not enabled. This message can
be ignored because it is not needed.

Now the running QEMU can be killed and the test image removed again:

    killall qemu-system-x86_64 # in a separate shell
    rm _work/clear-kvm-test.img
    reset # Clear Linux changes terminal colors, undo that.

The `clear-kvm` images are prepared automatically by the Makefile. By
default, four different images are prepared. Each image is pre-configured with
its own hostname and with network settings for the corresponding `tap`
device. `clear-kvm.img` is a symlink to the `clear-kvm.0.img` where
the Kubernetes master node will run.

The images will contain the latest
[Clear Linux OS](https://clearlinux.org/) and have the Kubernetes
version supported by Clear Linux installed.

### Starting and stopping a test cluster

`make start` will bring up a Kubernetes test cluster inside four QEMU
virtual machines. It can be called multiple times in a row and will
attempt to bring up missing pieces each time it is invoked.
The first node `host-0` is the Kubernetes master without persistent memory.
The other three nodes are worker nodes with one emulated 32GB NVDIMM each.
After the cluster has been formed, `make start` adds `storage=pmem` label
to the worker nodes and deploys the PMEM-CSI driver.
Once `make start` completes, the cluster is ready for interactive use via
`kubectl` inside the virtual machine. Alternatively, you can also
set `KUBECONFIG` as shown at the end of the `make start` output
and use `kubectl` binary on the host running VMs.

Use `make stop` to stop the virtual machines. The cluster state
remains preserved and will be restored after next `make start`.

The DeviceMode (lvm or direct) used in testing is selected using variable TEST_DEVICEMODE in [test-config.sh](test/test-config.sh).

### Running commands on test cluster nodes over ssh

`make start` generates ssh-wrappers `_work/ssh-clear-kvm.N` for each
test cluster node which are handy for running a single command or to
start interactive shell. Examples:

`_work/ssh-clear-kvm.0 kubectl get pods` runs a kubectl command on
node-0 which is cluster master.

`_work/ssh-clear-kvm.1` starts a shell on node-1.

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
$ KUBECONFIG=$(pwd)/_work/clear-kvm-kube.config REPO_ROOT=$(pwd) ginkgo -v -focus=pmem-csi.*should.provision.storage.with.defaults ./test/e2e/
Nov 26 11:21:28.805: INFO: The --provider flag is not set.  Treating as a conformance test.  Some tests may not be run.
Running Suite: PMEM E2E suite
=============================
Random Seed: 1543227683 - Will randomize all specs
Will run 1 of 61 specs

Nov 26 11:21:28.812: INFO: checking config
Nov 26 11:21:28.812: INFO: >>> kubeConfig: /nvme/gopath/src/github.com/intel/pmem-csi/_work/clear-kvm-kube.config
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

## Communication and contribution

Report a bug by [filing a new issue](https://github.com/intel/pmem-csi/issues).

Contribute by [opening a pull request](https://github.com/intel/pmem-csi/pulls).

Learn [about pull requests](https://help.github.com/articles/using-pull-requests/).

<!-- FILL TEMPLATE:
Contact the development team (*TBD: slack or email?*)


  ## References

Pointers to other useful documentation.

* Video tutorial
    * Simple youtube style. Demo installation following steps in readme.
      Useful to show relevant paths. Helps with troubleshooting.
-->
