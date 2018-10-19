<!-- based on template now, remaining parts marked as FILL TEMPLATE:  -->
# Intel PMEM-CSI plugin for Kubernetes

## About

This is PMEM-CSI driver for provisioning of node-local non-volatile memory to Kubernetes as block devices. The driver can currently utilize non-volatile memory devices which can be controlled via [libndctl utility library](https://github.com/pmem/ndctl).

The PMEM-CSI driver follows [CSI specification](https://github.com/container-storage-interface/spec) listening for API requests and provisioning volumes accordingly.

See also [K8s plugin definition](https://github.com/kubernetes/community/blob/master/contributors/design-proposals/resource-management/device-plugin.md)

## Design

* Architecture and Operation

This PMEM-CSI driver uses LVM for Logical Volumes management to avoid the risk of fragmentation that would become an issue if NVDIMM Namespaces would be served directly. The Logical Volumes of LVM are served to satisfy API requests. This also ensures the Region-affinity of served volumes, because Physical Volumes on each Region belong to a region-specific LVM Volume Group.

Currently the driver consists of three separate binaries that form 2 initialization stages and third API-serving stage.

During startup, the driver scans non-volatile memory for regions and namespaces, and tries to create more namespaces using all or part (selectable via option) of remaining available NVDIMM space. The Namespace size can be specified as driver parameter and defaults to 32 GB. This first stage is performed by separate entity pmem-ns-init.

The 2nd stage of initialization arranges Physical Volumes provided by Namespaces, into LVM Volume Groups. This is performed by separate binary pmem-vgm.

After two initialization stages the 3rd binary pmem-csi-driver starts serving CSI API requests.

* How plugin fits into the K8s big picture

Current implementation of PMEM-CSI plugin has node-local scope only, means that some higher-level orchestration will be additionally needed to achieve the distributed operation goals.

<!-- FILL TEMPLATE:
* Target users and use cases
* Design decisions & tradeoffs that were made
* What is in scope and outside of scope
-->

## Prerequisites

* Software required

So far, the early development and verification has mostly been carried out on qemu-emulated NVDIMMs, with brief testing on a system having real 2x256 GB of persistent memory

Build has been verified on a system described below.

- Host: Dell Poweredge R620, distro: openSUSE Tumbleweed, kernel 4.18.8, qemu 2.12.1, 3.0.0
- Guest: VM: 16GB RAM, 8 vCPUs, Ubuntu 18.04.1 server, kernel 4.15.0
- VM config originally created by libvirt/GUI (also doable using virt-install CLI), with configuration changes made directly in VM-config xml file to emulate pair of NVDIMMs backed by host file.

### NOTE about hugepages:

Emulated nvdimm appears not fully working if the VM is configured to use Hugepages. With Hugepages configured, no data survives guest reboots, nothing is ever written into backing store file in host. Configured backing store file is not even part of command line options to qemu. Instead of that, there is some dev/hugepages/libvirt/... path which in reality remains empty in host.

### maxMemory

```
  <maxMemory slots='16' unit='KiB'>33554432</maxMemory>
```

### NUMA config, 2 nodes with 8GB mem in both

```
  <cpu ...>
    <...>
    <numa>
      <cell id='0' cpus='0-3' memory='8388608' unit='KiB'/>
      <cell id='1' cpus='4-7' memory='8388608' unit='KiB'/>
    </numa>
  </cpu>
```

### Emulated 2x 1G NVDIMM with labels support

```
    <memory model='nvdimm' access='shared'>
      <source>
        <path>/var/lib/libvirt/images/nvdimm0</path>
      </source>
      <target>
        <size unit='KiB'>1048576</size>
        <node>0</node>
        <label>
          <size unit='KiB'>2048</size>
        </label>
      </target>
      <address type='dimm' slot='0'/>
    </memory>
    <memory model='nvdimm' access='shared'>
      <source>
        <path>/var/lib/libvirt/images/nvdimm1</path>
      </source>
      <target>
        <size unit='KiB'>1048576</size>
        <node>1</node>
        <label>
          <size unit='KiB'>2048</size>
        </label>
      </target>
      <address type='dimm' slot='1'/>
    </memory>
```

### 2x 1 GB NVDIMM backing file creation example on Host

```
dd if=/dev/zero of=/var/lib/libvirt/images/nvdimm0 bs=4K count=262144
dd if=/dev/zero of=/var/lib/libvirt/images/nvdimm1 bs=4K count=262144
```

### Labels initialization is needed once per emulated NVDIMM

The first OS startup in currently used dev.system with emulated NVDIMM triggers creation of one device-size pmem region and ndctl would show zero remaining available space. To make emulated NVDIMMs usable by ndctl, we use labels initialization steps which have to be run once after first bootup with new device(s).
These steps need to be repeated if device backing file(s) start from scratch.

For example, these commands for set of 2 NVDIMMs:
(can be re-written as loop for more devices)

```
ndctl disable-region region0
ndctl init-labels nmem0
ndctl enable-region region0

ndctl disable-region region1
ndctl init-labels nmem1
ndctl enable-region region1
```

## Software on the guest:
- Go: version 1.10.1
- ndctl: version 62, built on same host via autogen,configure,make,install steps as per instruction in README.md
- csc: built from github.com/rexray/gocsi 0.5.0
- Docker-ce: version 18.06.1

* Hardware required

Non-volatile DIMM device(s) are required for operation. Some development and testing can however be done using Qemu-emulated NVDIMMs.

## Supported Kubernetes versions

The driver deployment in kubernetes cluster has been verified on:

| Branch            | Kubernetes branch/version      |
|-------------------|--------------------------------|
| master            | Kubernetes 1.11 branch v1.11.3 |
| master            | Kubernetes 1.12 branch v1.12.1 |


## Setup

### Get source code

1. Use the `git clone https://github.com/otcshare/Pmem-CSI pmem-csi`

### NOTE!! The repository name differs from used path name

Paths of this package written in the code, are lowercase-only
so please be aware that code of this driver should reside
on the path `github.com/intel/pmem-csi/`

Either specify a different destination path when cloning:

`git clone .../Pmem-CSI pmem-csi`

or rename the directory from `Pmem-CSI` to `pmem-csi` after cloning.


### Build plugin

1. Use `make`

This produces the below binaries in _output directory:
- `pmem-ns-init`: Helper utility for namespace inititialization
- `pmem-vgm`: Helper utility for creating logical volume groups over pmem devices created.
- `pmem-csi-driver`: The PMEM csi driver

2. Use `make build-images`

This produces docker container images.

3. Use `make push-images`

This pushes docker container images to the docker images registry.

### Verify socket

1. You can verify that drivers listens on specified endpoint (Unix or TCP socket)

`netstat -l`

(if netstat is present in the system)

### Run plugin

#### Run driver as standalone
Use `run_driver` as user:root.
This runs two preparation parts, and starts driver binary which listens and
responds to API use on TCP or Unix socket.

Try volume lifecycle on a standalone system:

There are example steps forming a simple volume lifecycle in `run_client_csc`. 
The client-side endpoint can be specified either:

- with each csc command as `--endpoint tcp://127.0.0.1:10000` 
- export endpoint as env.variable, see `run_client_csc`

#### Run as kubernetes deployment

Use manifest file:  `kubectl create -f deploy/k8/pmem-csi.yaml`

##### Notes:
* The images registry address in the manifest is not final and may need tuning.
* A label "storage: nvdimm" needs to be added to a cluster node which provides NVDIMM device(s).

### Verify plugin is registered

<!-- FILL TEMPLATE:
1. Use the `blah` command.
-->

## Test plugin

# Test

1. Use the `make test` command.
Note: testing code is not completed yet.

## Use plugin in your application
<!-- FILL TEMPLATE:
[comment]: # (See the NVIDIA GPU example -> TensorFlow Jupyter Notebook)
1. Use the `blah` command.
-->

### How to extend the plugin

<!-- FILL TEMPLATE:
You can modify the plugin to support more xxx by changing the `variable` from Y to Z.
-->

## Maintenance

<!-- FILL TEMPLATE:
* Known limitations
* What is supported and what isn't supported
    * Disclaimer that nothing is supported with any kind of SLA
* Example configuration for target use case
* How to upgrade
* Upgrade cadence
-->

## Troubleshooting

<!-- FILL TEMPLATE:
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
