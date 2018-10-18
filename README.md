# PMEM CSI Driver

This is a draft version of the PMEM CSI driver.

## NOTE!! The repository name differs from used path name

Paths of this package written in the code, are lowercase-only
so please be aware that code of this driver should reside
on the path `github.com/intel/pmem-csi/`

Either specify a different destination path when cloning:

`git clone .../Pmem-CSI pmem-csi`

or rename the directory from `Pmem-CSI` to `pmem-csi` after cloning.

## State

The code builds, runs as standalone, see `run_driver`
and responds to API use on TCP or Unix socket.

# Development system configuration

So far, the development and verification has mostly been carried out on qemu-emulated NVDIMMs, with brief testing on a system having real 2x256 GB of persistent memory

Build has been verified on a system described below.

Running as stand-alone: There are example steps forming a simple volume lifecycle, using csc on same system. See steps using 'csc' in `run_client_csc`. The exact steps sequence does currently succeed due recent changes about k8s deployment.

- Host: Dell Poweredge R620, distro: openSUSE Tumbleweed, kernel 4.18.5, qemu 2.12.1
- Guest: VM: 16GB RAM, 8 vCPUs, Ubuntu 18.04.1 server, kernel 4.15.0
- VM config originally created by libvirt/GUI (also doable using virt-install CLI), with additions below to emulate 2x 1 GB NVDIMM backed by host file:

## Guest config additions, made directly in xml file

### NOTE about hugepages:

Emulated nvdimm does not fully work if VM is configured to use Hugepages. It seems to be bug or limitation in qemu. With Hugepages configured, no data survives guest reboots, nothing is ever written into backing store file in host. Configured backing store file is not even part of command line options to qemu. Instead of that, there is some dev/hugepages/libvirt/... path which in reality remains empty in host.

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

The default startup emulated NVDIMM creates one device-size pmem region
and ndctl would show zero available space, so labels init. step is required once.
This has to be repeated if device backing file(s) start from scratch.

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

vendor/ directory should stay up-to-date.

# Build

`make`

This produces the below binaries in _output directory:
- `pmem-ns-init`: Helper utility for namespace inititialization
- `pmem-vgm`: Helper utility for creating logical volume groups over pmem devices created.
- `pmem-csi-driver`: The PMEM csi driver

## Docker-based build

`make build-images`

This succeeds and produces docker images and driver can be
started inside of that image:

`docker run IMAGE --endpoint tcp://127.0.0.1:10000`

Further container-based use scenarios have not been explored yet.

# Test

`make test`

# Run

1. start driver (as user:root) as in `run_driver`
2. Example client-side commands using csc (as regular user) as in `run_client_csc`

The client-side endpoint can be specified either:

- with each csc command as `--endpoint tcp://127.0.0.1:10000` 
- export endpoint as env.variable, see `run_client_csc`
