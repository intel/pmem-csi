<!-- based on template now, remaining parts marked as FILL TEMPLATE:  -->

# Intel PMEM-CSI plugin for Kubernetes

## Table of Contents

- [About](#about)
- [Design](#design)
- [Prerequisites](#prerequisites)
- [Supported Kubernetes versions](#supported-kubernetes-versions)
- [Setup](#setup)
- [Automated testing](#automated-testing)
- [Communication and contribution](#communication-and-contribution)

## About

---
*Note: This is Alpha code and not production ready.*
---

This [Kubernetes plugin](https://github.com/kubernetes/community/blob/master/contributors/design-proposals/resource-management/device-plugin.md) is a Persistent Memory Container Storage Interface (PMEM-CSI) driver for provisioning of node-local non-volatile memory to Kubernetes as block devices. The driver can currently utilize non-volatile memory devices that can be controlled via the [libndctl utility library](https://github.com/pmem/ndctl). In this readme, we use *persistent memory* to refer to a non-volatile dual in-line memory module (NVDIMM).

The PMEM-CSI driver follows the [CSI specification](https://github.com/container-storage-interface/spec) by listening for API requests and provisioning volumes accordingly.


## Design

### Architecture and Operation

The PMEM-CSI driver uses LVM for Logical Volumes Management to avoid the risk of fragmentation that would become an issue if Namespaces were served directly. The logical volumes of LVM are served to satisfy API requests. This also ensures the region-affinity of served volumes, because physical volumes on each region belong to a region-specific LVM volume group.

Currently the driver consists of three separate binaries that form two
initialization stages and a third API-serving stage.

During startup, the driver scans persistent memory for regions and namespaces, and tries to create more namespaces using all or part (selectable via option) of the remaining available space. The namespace size can be specified as a driver parameter and defaults to 32 GB. This first stage is performed by a separate entity _pmem-ns-init_.

The second stage of initialization arranges physical volumes provided by namespaces into LVM volume groups. This is performed by a separate binary _pmem-vgm_.

After two initialization stages, the third binary _pmem-csi-driver_ starts serving CSI API requests.

### Namespace modes

The PMEM-CSI driver can pre-create Namespaces in two modes, forming corresponding LVM Volume groups, to serve volumes based on `fsdax` or `sector` (alias `safe`) mode Namespaces. The amount of space to be used is determined using two options `-useforfsdax` and `-useforsector` given to _pmem-ns-init_. These options specify an integer presenting limit as percentage, which is applied separately in each Region. The default values are `useforfsdax=100` and `useforsector=0`. A CSI request for volume can specify the Namespace mode using the plugin-specific argument `nsmode` which has a value of either "fsdax" (default) or "sector". A volume provisioned in `fsdax` mode will have the `dax` option added to mount options.

**Note:** Currently, the kernel (tested with current latest 4.19.3) overrides the ext4-type filesystem `dax` mount option like this:
```
[  232.584035] EXT4-fs (dm-0): DAX enabled. Warning: EXPERIMENTAL, use at your own risk
[  232.584038] EXT4-fs (dm-0): DAX unsupported by block device. Turning off DAX.
[  232.585125] EXT4-fs (dm-0): mounted filesystem with ordered data mode. Opts: dax
```

This happens when Volume is provisioned on top of LVM. The override does not happen if a persistent memory phys.volume is directly formatted and mounted.

The similar override does not happen when volume is formatted as an XFS file system.

### Using limited amount of total space

The PMEM-CSI driver can leave space on devices for others, and recognize "own" namespaces. Leaving space for others can be achieved by specifying lower-than-100 values to `-useforfsdax` and/or `-useforsector` options. The distinction "own" vs. "foreign" is implemented by setting the _Name_ field in Namespace to a static string "pmem-csi" during Namespace creation. When adding Physical Volumes to Volume Groups, only Physical Volumes that are based on Namespaces with the name "pmem-csi" are considered.

### Driver modes

The PMEM-CSI driver supports running in different modes, which can be controlled by passing one of the below options to the driver's '_-mode_' command line option. In each mode, it starts a different set of open source Remote Procedure Call (gRPC) [servers](#driver-components) on given driver endpoint(s).

* **_Controller_**  mode is intended to be used in a multi-node cluster and should run as a single instance in cluster level. When the driver is running in _Controller_ mode, it forwards the pmem volume create/delete requests to the registered node controller servers running on the worker node. In this mode, the driver starts the following gRPC servers:

    * [IdentityServer](#identity-server)
    * [NodeRegistryServer](#node-registry-server)
    * [MasterControllerServer](#master-controller-server)

* **_Node_** mode is intended to be used in a multi-node cluster by worker nodes that have persistent memory devices installed. When the driver starts in this mode, it registers with the _Controller_ driver running on a given _-registryEndpoint_. In this mode, the driver starts the following servers:

    * [IdentityServer](#identity-server)
    * [NodeControllerServer](#node-controller-server)
    * [NodeServer](#node-server)

* **_Unified_** mode is intended to run the driver in a single host, mostly for testing the driver in a non-clustered environment.

### Driver Components

#### Identity Server

This gRPC server operates on a given endpoint in all driver modes and implements the CSI [Identity interface](https://github.com/container-storage-interface/spec/blob/master/spec.md#identity-service-rpc).

#### Node Registry Server

When the PMEM-CSI driver runs in _Controller_ mode, it starts a gRPC server on a given endpoint(_-registryEndpoint_) and serves the [RegistryServer](pkg/pmem-registry/pmem-registry.proto) interface. The driver(s) running in _Node_ mode can register themselves with node specific information such as node id, [NodeControllerServer](#node-controller-server) endpoint, and their available persistent memory capacity.

#### Master Controller Server

This gRPC server is started by the PMEM-CSI driver running in _Controller_ mode and serves the [Controller](https://github.com/container-storage-interface/spec/blob/master/spec.md#controller-service-rpc) interface defined by the CSI specification. The server responds to CreateVolume(), DeleteVolume(), ControllerPublishVolume(), ControllerUnpublishVolume(), and ListVolumes() calls coming from [external-provisioner]() and [external-attacher]() sidecars. It forwards the publish and unpublish volume requests to the appropriate [Node controller server](#node-controller-server) running on a worker node that was registered with the driver.

#### Node Controller Server

This gRPC server is started by the PMEM-CSI driver running in _Node_ mode and implements the [ControllerPublishVolume](https://github.com/container-storage-interface/spec/blob/master/spec.md#controllerpublishvolume)  and [ControllerUnpublishVolume](https://github.com/container-storage-interface/spec/blob/master/spec.md#controllerunpublishvolume) methods of the [Controller service](https://github.com/container-storage-interface/spec/blob/master/spec.md#controller-service-rpc) interface defined by the CSI specification. It serves the ControllerPublishVolume() and ControllerUnpublish() requests coming from the [Master controller server](#master-controller-server) and creates/deletes persistent memory devices.

#### Node Server

This gRPC server is started by the driver running in _Node_ mode and implements the [Node service](https://github.com/container-storage-interface/spec/blob/master/spec.md#node-service-rpc) interface defined in the CSI specification. It serves the NodeStageVolume(), NodeUnstageVolume(), NodePublishVolume(), and NodeUnpublishVolume() requests coming from the Container Orchestrator (CO).

### Communication channels

The following diagram illustrates the communication channels between driver components:
![communication diagram](/docs/images/communication/pmem-csi-communication-diagram.png)

### Security

All pmem-csi specific communication [shown in above section](#communication-channels) between Master Controller([RegistryServer](#node-registry-server), [MasterControllerServer](#master-controller-server)) and NodeControllers([NodeControllerServer](#node-controller-server)) is protected by mutual TLS. Both client and server must identify themselves and the certificate they present must be trusted. The common name in each certificate is used to identify the different components. The following common names have a special meaning:

- `pmem-registry` is used by the [RegistryServer](#node-registry-server).
- `pmem-node-controller` is used by [NodeControllerServers](#node-controller-server)

The [`test/setup-ca-kubernetes.sh`](test/setup-ca-kubernetes.sh) script shows how to generate certificates signed by Kubernetes cluster root Certificate Authority. And the provided [deployment files](deploy/kubernetes/pmem-csi.yaml) shows how to use the generated certificates to setup the driver. The test cluster is setup using certificates created by that script. The [`test/setup-ca.sh`](test/setup-ca.sh) script also shows how to generate self signed certificates. These are just examples, administrators of a cluster must ensure that they choose key lengths and algorithms of sufficient strength for their purposes and manage certificate distribution.

A production deployment can improve upon that by using some other key delivery mechanism, like for example [Vault](https://www.vaultproject.io/).

### Dynamic provisioning

The following diagram illustrates how the PMEM-CSI driver performs dynamic volume provisioning in Kubernetes:
![sequence diagram](/docs/images/sequence/pmem-csi-sequence-diagram.png)
<!-- FILL TEMPLATE:
* Target users and use cases
* Design decisions & tradeoffs that were made
* What is in scope and outside of scope
-->

## Prerequisites

### Software required

Building has been verified using these components:

- Go: version 1.10.1
- [ndctl](https://github.com/pmem/ndctl) version 62, built on dev.host via autogen, configure, make, and install as per instruction in README.md

Building of Docker images has an additional requirement:

- Docker-ce: verified using version 18.06.1

### Hardware required

Persistent memory device(s) are required for operation. However, some development and testing can be done using QEMU-emulated persistent memory devices, see [README-qemu-notes](README-qemu-notes.md).

### Persistent memory device initialization

The driver does not create persistent memory Regions, but expects Regions to exist when the driver starts. The utility [ipmctl](https://github.com/intel/ipmctl) can be used to create Regions.

## Supported Kubernetes versions

The driver deployment in Kubernetes cluster has been verified on:

| Branch            | Kubernetes branch/version      |
|-------------------|--------------------------------|
| devel             | Kubernetes 1.11 branch v1.11.3 |

## Setup

### Development system using Virtual Machine

Early development and verification was performed on QEMU-emulated persistent memory devices.

The build was verified on the system described below:

* Host: Dell Poweredge R620, distro: openSUSE Leap 15.0, kernel 4.12.14, qemu 2.11.2
* Guest VM: 32GB RAM, 8 vCPUs, Ubuntu 18.04.1 server, kernel 4.15.0, 4.18.0, 4.19.1
* See [README-qemu-notes](README-qemu-notes.md) for more details about VM config

### Get source code

Use the command: `git clone https://github.com/intel/pmem-csi`

### Build plugin

1.  Use `make`

    This produces the following binaries in the `_output` directory:

    * `pmem-ns-init`: Helper utility for namespace initialization.
    * `pmem-vgm`: Helper utility for creating logical volume groups over PMEM devices created.
    * `pmem-csi-driver`: PMEM-CSI driver.

2.  Use `make build-images` to produce Docker container images.

3.  Use `make push-images` to push Docker container images to a Docker images registry. The
    default is to push to a local [Docker registry](https://docs.docker.com/registry/deploying/).

See the [Makefile](Makefile) for additional make targets and possible make variables.

### Run plugin

#### Run driver as standalone

This is useful in development/trial mode.

Use `run_driver` as user:root.
This runs two preparation parts, and starts the driver binary, which listens and responds to API use on a TCP socket. You can modify this to use a Unix socket, if needed.

#### Run as Kubernetes deployment

This section assumes that a Kubernetes cluster is already available
with at least one node that has persistent memory device(s). For development or
testing, it is also possible to use a cluster that runs on QEMU virtual
machines, see the ["End-to-end testing"](#end-to-end-testing) section below.

- **Label the cluster nodes that have persistent memory support**

```sh
    $ kubectl label node pmem-csi-4 storage=pmem
```

The label **storage: pmem** needs to be added to the cluster node that provides persistent memory device(s).

Clusters with multiple nodes with persistent memory are not fully supported at
the moment. Support for this will be added when making the CSI driver topology-
aware.

- **Deploy the driver to Kubernetes**

```sh
    $ sed -e 's/192.168.8.1:5000/<your registry>/' deploy/kubernetes-1.12/pmem-csi.yaml | kubectl create -f -
```

This yaml file uses the registry address for the QEMU test cluster
setup (see below). When deploying on a real cluster, some registry
that can be accessed by that cluster has to be used.

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

You can modify the plugin to support more xxx by changing the `variable` from Y to Z.


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

### Verify driver in unified mode

The driver can be verified in the single-host context. This running mode is called "Unified"
in the driver. Both Controller and Node service run combined in local host, without Kubernetes context.

The endpoint for driver access can be specified either:

* with each csc command as `--endpoint tcp://127.0.0.1:10000`
* export endpoint as env.variable, see `util/lifecycle-unified.sh`

These run-time dependencies are used by the plugin in Unified mode:

- lvm2
- shred
- mount
- file
- blkid

#### Scripts in util/ directory

* [lifecycle-unified](util/lifecycle-unified.sh) example steps verifying a volume lifecycle
* [sanity-unified](util/sanity-unified.sh) API test using csi-sanity
* [get-capabilities-unified](util/get-capabilities-unified.sh) Query Controller and Node capabilities

These utilities are required by scripts residing in `util/` directory:

- [csc](http://github.com/rexray/gocsi) v0.5.0
- [csi-sanity](http://github.com/kubernetes-csi/csi-test) v0.2.0-1-95-g3bc4135

### End-to-end testing (E2E)

#### QEMU + Kubernetes

E2E testing relies on a cluster running inside multiple QEMU virtual
machines. This is known to work on a Linux development host system.
The `qemu-system-x86_64` binary must be installed, either from
[upstream QEMU](https://www.qemu.org/) or the Linux distribution.

For networking, the `ip` tool from the `iproute2` package must be
installed. The following command must be run once after booting the
host machine and before starting the virtual machine:

    test/runqemu-ifup 4

This configures four tap devices for use by the current user. At the
moment, the test setup uses:

- `pmemtap0/1/2/3`
- `pmembr0`
- 192.168.7.1 for the build host side of the bridge interfaces
- 192.168.7.2/4/6/8 for the virtual machines
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

#### Starting and stopping a test cluster

`make start` will bring up a Kubernetes test cluster inside four QEMU
virtual machines. It can be called multiple times in a row and will
attempt to bring up missing pieces each time it is invoked.

Once it completes, everything is ready for interactive use via
`kubectl` inside the virtual machine. Alternatively, you can also
set `KUBECONFIG` as shown at the end of the `make start` output
and use a local `kubectl` binary.

The first node is the Kubernetes master without persistent memory. The other
three nodes are worker nodes with one 32GB NVDIMM each. The worker nodes have
already been labeled with `storage=pmem`, but the pmem-csi driver still needs to be installed manually as shown in ["Run as Kubernetes deployment"](#run-as-
kubernetes-deployment). If the Docker registry runs on the local development
host, then the `sed` command which replaces the Docker registry is not needed.

Once done, `make stop` will clean up the cluster and shut everything down.

#### Running E2E tests

`make test_e2e` will run
[csi-test sanity](https://github.com/kubernetes-csi/csi-test/tree/master/pkg/sanity)
tests and some
[Kubernetes storage tests](https://github.com/kubernetes/kubernetes/tree/master/test/e2e/storage/testsuites)
against the pmem-csi driver.

The driver will get deployed automatically and thus must not be
installed yet on the cluster.

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

* Upstream documentation of [device plugins](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/).
* Video tutorial
    * Simple youtube style. Demo installation following steps in readme.
      Useful to show relevant paths. Helps with troubleshooting.
-->
