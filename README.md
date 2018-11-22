<!-- based on template now, remaining parts marked as FILL TEMPLATE:  -->

# Intel PMEM-CSI plugin for Kubernetes

## About

---
*Note: This is Alpha code and not production ready.*
---

This [Kubernetes plugin](https://github.com/kubernetes/community/blob/master/contributors/design-proposals/resource-management/device-plugin.md) is a Persistent Memory Container Storage Interface (PMEM-CSI) driver for provisioning of node-local non-volatile memory to Kubernetes as block devices. The driver can currently utilize non-volatile memory devices which can be controlled via [libndctl utility library](https://github.com/pmem/ndctl).

The PMEM-CSI driver follows the [CSI specification](https://github.com/container-storage-interface/spec) by listening for API requests and provisioning volumes accordingly.


## Design

### Architecture and Operation

The PMEM-CSI driver uses LVM for Logical Volumes Management to avoid the risk of fragmentation that would become an issue if NVDIMM Namespaces were served directly. The logical volumes of LVM are served to satisfy API requests. This also ensures the region-affinity of served volumes, because physical volumes on each region belong to a region-specific LVM volume group.

Currently the driver consists of three separate binaries that form two
initialization stages and a third API-serving stage.

During startup, the driver scans non-volatile memory for regions and namespaces, and tries to create more namespaces using all or part (selectable via option) of remaining available NVDIMM space. The namespace size can be specified as a driver parameter and defaults to 32 GB. This first stage is performed by a separate entity _pmem-ns-init_.

The second stage of initialization arranges physical volumes provided by namespaces, into LVM volume groups. This is performed by a separate binary _pmem-vgm_.

After two initialization stages, the third binary _pmem-csi-driver_ starts serving CSI API requests.

### Driver modes

The PMEM-CSI driver supports running in different modes, which can be controlled by passing one of the below options to the driver's '_-mode_' command line option. In each mode it starts a different set of open source Remote Procedure Call (gRPC) [servers](#driver-components) on given driver endpoint(s).

* **_Controller_**  mode is intended to be used in a multi-node cluster and should run as single instance in cluster level. When the driver is running in _Controller_ mode, it forwards the pmem volume create/delete requests to the registered node controller servers running on the worker node. In this mode, the driver starts the following gRPC servers:

    * [IdentityServer](#identity-server)
    * [NodeRegistryServer](#node-registry-server)
    * [MasterControllerServer](#master-controller-server)

* **_Node_** mode is intended to be used in a multi-node cluster by worker nodes that have NVDIMMs devices installed. When the driver starts in this mode, it registers with the _Controller_ driver running on given _-registryEndpoint_. In this mode, the driver starts the following servers:

    * [IdentityServer](#identity-server)
    * [NodeControllerServer](#node-controller-server)
    * [NodeServer](#node-server)

* **_Unified_** mode is intended to run the driver in a single host, mostly for testing the driver in a non-clustered environment.

### Driver Components

#### Identity Server

This gRPC server serves on a given endpoint in all driver modes and implements the CSI [Identity interface](https://github.com/container-storage-interface/spec/blob/master/spec.md#identity-service-rpc).

#### Node Registry Server

When the PMEM-CSI driver runs in _Controller_ mode, it starts a gRPC server on given endpoint(_-registryEndpoint_) and serves [RegistryServer](pkg/pmem-registry/pmem-registry.proto) interface. The driver(s) running in _Node_ mode can register themselves with node specific information such as node id, [NodeControllerServer](#node-controller-server) endpoint, and available NVDIMM capacity with them.

#### Master Controller Server

This gRPC server is started by the PMEM-CSI driver running in _Controller_ mode and serves [Controller](https://github.com/container-storage-interface/spec/blob/master/spec.md#controller-service-rpc) interface defined by CSI specification. The server responds to CreateVolume(), DeleteVolume(), ControllerPublishVolume(), ControllerUnpublishVolume(), and ListVolumes() calls coming from [external-provisioner]() and [external-attacher]() sidecars. It forwards the publish and unpublish volume requests to the appropriate [Node controller server](#node-controller-server) running on a worker node that was registered with the driver.

#### Node Controller Server

This gRPC server is started by the PMEM-CSI driver running in _Node_ mode and implements [ControllerPublishVolume](https://github.com/container-storage-interface/spec/blob/master/spec.md#controllerpublishvolume)  and [ControllerUnpublishVolume](https://github.com/container-storage-interface/spec/blob/master/spec.md#controllerunpublishvolume) methods of the [Controller service](https://github.com/container-storage-interface/spec/blob/master/spec.md#controller-service-rpc) interface defined by CSI specification. It serves the ControllerPublishVolume() and ControllerUnpublish() requests coming from the [Master controller server](#master-controller-server) and create/delete PMEM devices.

#### Node Server

This gRPC server is started by the driver running in _Node_ mode and implements the [Node service](https://github.com/container-storage-interface/spec/blob/master/spec.md#node-service-rpc) interface defined in the CSI specification. It serves the NodeStageVolume(), NodeUnstageVolume(), NodePublishVolume(), and NodeUnpublishVolume() requests coming from the Container Orchestrator (CO).

### Communication channels

The following diagram illustrates the communication channels between driver components:
![communication diagram](/docs/images/communication/pmem-csi-communication-diagram.png)

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
- [ndctl](https://github.com/pmem/ndctl) version 62, built on dev.host via autogen,configure,make,install as per instruction in README.md

Building of Docker images has additionally requires:

- Docker-ce: verified using version 18.06.1

### Hardware required

Non-volatile DIMM device(s) are required for operation. Some development and testing can however be done using QEMU-emulated NVDIMMs, see [README-qemu-notes](README-qemu-notes.md).

### NVDIMM device initialization

The driver does not create NVDIMM Regions, but expects Regions to exist when the driver starts. The utility [ipmctl](https://github.com/intel/ipmctl) can be used to create Regions.

## Supported Kubernetes versions

The driver deployment in Kubernetes cluster has been verified on:

| Branch            | Kubernetes branch/version      |
|-------------------|--------------------------------|
| devel             | Kubernetes 1.11 branch v1.11.3 |

## Setup

### Development system using Virtual Machine

Early development and verification was performed on QEMU-emulated NVDIMMs.

The build was verified on the system described below:

* Host: Dell Poweredge R620, distro: openSUSE Leap 15.0, kernel 4.12.14, qemu 2.11.2
* Guest VM: 32GB RAM, 8 vCPUs, Ubuntu 18.04.1 server, kernel 4.15.0, 4.18.0, 4.19.1
* See [README-qemu-notes](README-qemu-notes.md) for more details about VM config

### Get source code

Use the command: `git clone https://github.com/otcshare/Pmem-CSI csi-pmem`

> **NOTE:** The repository name is mixed-case but the paths are
> lowercase-only. To build the plugin, the driver code must reside on the path github.com/intel/csi-pmem/
>
> You must specify a different destination path when cloning:
> `git clone .../Pmem-CSI csi-pmem`
> or rename the directory from `Pmem-CSI` to `csi-pmem` after cloning.

### Build plugin

1.  Use `make`

    This produces the following binaries in the `_output` directory:

    * `pmem-ns-init`: Helper utility for namespace initialization.
    * `pmem-vgm`: Helper utility for creating logical volume groups over PMEM devices created.
    * `pmem-csi-driver`: The PMEM-CSI driver.

2.  Use `make build-images` to produce Docker container images.

3.  Use `make push-images` to push Docker container images to the Docker images registry.

See the [Makefile](Makefile) for additional make targets and possible make variables.

### Verify socket

Verify that the driver listens on the specified endpoint (Unix or TCP socket) with the command:
`netstat -l`

This command assumes that netstat is present in the system.

### Run plugin

#### Run driver as standalone

This is useful in development/trial mode.

Use `run_driver` as user:root.
This runs two preparation parts, and starts the driver binary, which listens and responds to API use on a TCP socket. You can modify this to use a Unix socket, if needed.

#### Run as Kubernetes deployment

- **Label the cluster nodes that have NVDIMM support**

```sh
    $ kubectl label node pmem-csi-4 storage=nvdimm
    $ kubectl label node pmem-csi-5 storage=nvdimm
```

- **Deploy the driver to Kubernetes**

```sh
    $ kubectl create -f deploy/k8s/pmem-csi.yaml
```

- **Provision a csi-pmem volume**

```sh
    $ kubectl create -f deploy/k8s/pmem-pvc.yaml
```

- **Start an application requesting provisioned volume**

```sh
    $ kubectl create -f deploy/k8s/pmem-app.yaml
```

- **Once the application pod is in 'Running' status, provision it with a pmem volume**

```sh
    $ kubectl get po my-csi-app
    NAME                        READY     STATUS    RESTARTS   AGE
    my-csi-app                  1/1       Running   0          1m
    
    $ kubectl exec my-csi-app -- df /data
    Filesystem           1K-blocks      Used Available Use% Mounted on
    /dev/ndbus0region0/7a4cc7b2-ddd2-11e8-8275-0a580af40161
                           8191416     36852   7718752   0% /data
```

**Notes:**

* The images registry address in the manifest is not final and may need tuning.
* A label **storage: nvdimm** needs to be added to the cluster node that provides NVDIMM device(s).
* The application should add **storage: nvdimm** to its <i>nodeSelector</i> list.


## Test plugin

### Run tests using make

1. Use the `make test` command.

Note: Testing code is not completed yet. Currently it runs some passes using `gofmt, go vet`.

### Verify driver in unified mode

The driver can be verified in the single-host context. Such running mode is called "Unified"
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

These utilities are needed as used by scripts residing in `util/` directory:

- [csc](http://github.com/rexray/gocsi) v0.5.0
- [csi-sanity](http://github.com/kubernetes-csi/csi-test) v0.2.0-1-95-g3bc4135

<!-- FILL TEMPLATE:

## Use plugin in your application

[comment]: # (See the NVIDIA GPU example -> TensorFlow Jupyter Notebook)
1. Use the `blah` command.


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

## Communication and contribution

Report a bug by [filing a new issue](https://github.com/otcshare/Pmem-CSI/issues).

Contribute by [opening a pull request](https://github.com/otcshare/Pmem-CSI/pulls).

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
