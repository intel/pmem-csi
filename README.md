<!-- based on template now, remaining parts marked as FILL TEMPLATE:  -->

# Intel PMEM-CSI plugin for Kubernetes

## About

This is PMEM-CSI driver for provisioning of node-local non-volatile memory to Kubernetes as block devices. The driver can currently utilize non-volatile memory devices which can be controlled via [libndctl utility library](https://github.com/pmem/ndctl).

The PMEM-CSI driver follows [CSI specification](https://github.com/container-storage-interface/spec) listening for API requests and provisioning volumes accordingly.

## Design

### Architecture and Operation

This PMEM-CSI driver uses LVM for Logical Volumes management to avoid the risk of fragmentation that would become an issue if NVDIMM Namespaces would be served directly. The Logical Volumes of LVM are served to satisfy API requests. This also ensures the Region-affinity of served volumes, because Physical Volumes on each Region belong to a region-specific LVM Volume Group.

Currently the driver consists of three separate binaries that form 2 initialization stages and third API-serving stage.

During startup, the driver scans non-volatile memory for regions and namespaces, and tries to create more namespaces using all or part (selectable via option) of remaining available NVDIMM space. The Namespace size can be specified as driver parameter and defaults to 32 GB. This first stage is performed by separate entity _pmem-ns-init_.

The 2nd stage of initialization arranges Physical Volumes provided by Namespaces, into LVM Volume Groups. This is performed by separate binary _pmem-vgm_.

After two initialization stages the 3rd binary _pmem-csi-driver_ starts serving CSI API requests.

### Driver modes

The pmem csi driver supports running in different modes, which can be controlled by passing one of the below options to driver's '_-mode_' command line option. In each mode it starts different set of gRPC [servers](#components) on given driver endpoint(s).

> **_Controller_**  mode is intended to use in multi-node cluster and should run as single instance in cluster level. When the driver running in _Controller_ mode it forwards the pmem volume create, delete requests to the registered node controller servers running on worker node. The driver starts below gRPC servers when running in this mode:

- [IdentityServer](#identity-server)
- [NodeRegistryServer](#node-registry-server)
- [MasterControllerServer](#master-controller-server)

> **_Node_** mode is intended to use in multi-node cluster by worker nodes which has NVDIMMs devices installed. When the driver starts in this mode, it registers with the _Controller_ driver running on given _-registryEndpoint_. In this mode driver starts below servers:

- [IdentityServer](#identity-server)
- [NodeControllerServer](#node-controller-server)
- [NodeServer](#node-server)

> **_Unified_** mode is intended to run the driver in a single-node cluster, mostly for testing the driver on non-clustered environment.

### Driver Components

#### Identity Server

This is a gRPC server, serves on given endpoint in all driver modes and implements the csi [Identity interface](https://github.com/container-storage-interface/spec/blob/master/spec.md#identity-service-rpc).

#### Node Registry Server

When the PMEM csi driver runs in _Controller_ mode, it starts a gRPC server on given endpoint(_-registryEndpoint_) and serves [RegistryServer](pkg/pmem-registry/pmem-registry.proto) interface. The driver(s) running in _Node_ mode can register themselves with node specific information such as node id, [NodeControllerServer](#node-controller-server) endpoint and available nvdimm capacity with them.

#### Master Controller Server

This is a gRPC server starts by the pmem csi driver running in _Controller_ mode and serves [Controller](https://github.com/container-storage-interface/spec/blob/master/spec.md#controller-service-rpc) interface defined by CSI specification. The server responds to CreateVolume(), DeleteVolume(), ControllerPublishVolume(), ControllerUnpublishVolume() and ListVolumes() calls coming from [external-provisioner]() and [external-attacher]() sidecars. It forwards the publish and unpublish volume request to appropriate [Node controller server](#node-controller-server) running on a worker node that was registered with the driver.

#### Node Controller Server

This is a gRPC server that starts by the pmem csi driver running in _Node_ mode and implements [ControllerPublishVolume](https://github.com/container-storage-interface/spec/blob/master/spec.md#controllerpublishvolume)  and [ControllerUnpublishVolume](https://github.com/container-storage-interface/spec/blob/master/spec.md#controllerunpublishvolume) methods of [Controller service](https://github.com/container-storage-interface/spec/blob/master/spec.md#controller-service-rpc) interface defined by CSI specification. It serves the ControllerPublishVolume() and ControllerUnpublish() requests coming from [Master controller server](#master-controller-server) and create/delete pmem devices.

#### Node Server

This is a gRPC server starts by the driver when running in _Node_ mode and implements [Node service](https://github.com/container-storage-interface/spec/blob/master/spec.md#node-service-rpc) interface defined in CSI specification. It serves the NodeStageVolume(), NodeUnstageVolume(), NodePublishVolume(), NodeUnpublishVolume() requests coming from CO(Container Orchestrator).

### Dynamic provisioning

The below diagram illustrates how does dynamic volume provisioning with PMEM csi driver works in kubernetes:
![sequence diagram](/docs/images/sequence/pmem-csi-sequence-diagram.png)
<!-- FILL TEMPLATE:
* Target users and use cases
* Design decisions & tradeoffs that were made
* What is in scope and outside of scope
-->

## Prerequisites

### Software required

Building and Unified-mode run has been verified using these components:

- Go: version 1.10.1
- [ndctl](https://github.com/pmem/ndctl) version 62, built on dev.host via autogen,configure,make,install as per instruction in README.md
- Docker-ce: version 18.06.1

### Hardware required

Non-volatile DIMM device(s) are required for operation. Some development and testing can however be done using Qemu-emulated NVDIMMs, see [README-qemu-notes](README-qemu-notes.txt).

## Supported Kubernetes versions

The driver deployment in kubernetes cluster has been verified on:

| Branch            | Kubernetes branch/version      |
|-------------------|--------------------------------|
| devel             | Kubernetes 1.11 branch v1.11.3 |

## Setup

### Development system

So far, the early development and verification has mostly been carried out on qemu-emulated NVDIMMs, with brief testing on a system having real 2x256 GB of persistent memory

* Host: Dell Poweredge R620, distro: openSUSE Leap 15.0, kernel 4.12.14, qemu 2.11.2
* Guest VM: 32GB RAM, 8 vCPUs, Ubuntu 18.04.1 server, kernel 4.15.0, 4.18.0, 4.19.1
* See [README-qemu-notes](README-qemu-notes.txt) for more details about VM config

### Get source code

1. Use the `git clone https://github.com/otcshare/Pmem-CSI pmem-csi`

> **NOTE:** The repository name differs from used path name

Paths of this package written in the code, are lowercase-only
so please be aware that code of this driver should reside
on the path `github.com/intel/pmem-csi/`

Either specify a different destination path when cloning:

`git clone .../Pmem-CSI pmem-csi`

or rename the directory from `Pmem-CSI` to `pmem-csi` after cloning.

### Build plugin

1. Use `make`

This produces the below binaries in _output directory:

- `pmem-ns-init`: Helper utility for namespace initialization
- `pmem-vgm`: Helper utility for creating logical volume groups over pmem devices created.
- `pmem-csi-driver`: The PMEM csi driver

2. Use `make build-images`

This produces docker container images.

3. Use `make push-images`

This pushes docker container images to the docker images registry.

See [Makefile](Makefile) for additional make targets and possible make variables.

### Verify socket

1. You can verify that driver listens on specified endpoint (Unix or TCP socket)

`netstat -l`

(if netstat is present in the system)

### Run plugin

#### Run driver as standalone

This is useful in development mode.

Use `run_driver` as user:root.
This runs two preparation parts, and starts driver binary which listens and
responds to API use on a TCP socket. You can modify this to use Unix socket if needed.

#### Run as kubernetes deployment

- **Label the cluster nodes which has nvdimm support**

```sh
    $ kubectl label node pmem-csi-4 storage=nvdimm
    $ kubectl label node pmem-csi-5 storage=nvdimm
```

- **Deploy the driver to kubernetes**

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

- **Once the application pod in 'Running' status , it should have provisioned with a pmem volume**

```sh
    $ kubectl get po my-csi-app
    NAME                        READY     STATUS    RESTARTS   AGE
    my-csi-app                  1/1       Running   0          1m
    
    $ kubectl exec my-csi-app -- df /data
    Filesystem           1K-blocks      Used Available Use% Mounted on
    /dev/ndbus0region0/7a4cc7b2-ddd2-11e8-8275-0a580af40161
                           8191416     36852   7718752   0% /data
```

> **Notes:**
> - The images registry address in the manifest is not final and may need tuning.
> - A label **storage: nvdimm** needs to be added to a cluster node which provides NVDIMM device(s).
> - Application should add **storage: nvdimm** to its <i>nodeSelector</i> list

<!-- FILL TEMPLATE:
1. Use the `blah` command.
-->

## Test plugin

### Tests runnable via make

Use the `make test` command.

Note: testing code is not completed yet. Currently it runs some passes using `gofmt, go vet`.

### Verification in Unified mode

The driver can be verified in the single-host context. Such running mode is called "Unified"
in the driver. Both Controller and Node service run combined in local host, without Kubernetes context.

The endpoint for driver access can be specified either:

* with each csc command as `--endpoint tcp://127.0.0.1:10000`
* export endpoint as env.variable, see `util/lifecycle-unified.sh`

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
-->

## References

<!-- FILL TEMPLATE:
Pointers to other useful documentation.

* Upstream documentation of [device plugins](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/).
* Video tutorial
    * Simple youtube style. Demo installation following steps in readme.
      Useful to show relevant paths. Helps with troubleshooting.
-->
