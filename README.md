# PMEM CSI Driver

This is a draft version of the PMEM CSI driver.

## NOTE!! The repository name differs from used path name

Paths of this package written in the code, are lowercase-only
so please be aware that code of this driver should reside
on the path `github.com/intel/pmem-csi/`

Either specify a different destination path when cloning:

`git clone .../Pmem-CSI pmem-csi`

or rename the directory from `Pmem-CSI` to `pmem-csi` after cloning.

## Based on

The driver code is based on early OIM-CSI driver which in turn
was mostly based on the hostpath driver.

## Development stages and corresponding code branches

### First proof-of-concept trial

- was implemented in branch:devel, with down-interface as ndctl commands. ndctl command is performed, it's JSON-format output is unmarshalled into structured data.
- The driver does not maintain state, i.e. state of objects is looked up using down-interface.
- The "name" field is used as primary key and VolumeID.
- The "name" field value for a new namespace and mount point path names originate from csc CLI commands.
- ext4 or xfs is supported as file system type
- This trial had limited functionality and was used to demonstrate simple life cycle, and to verify dev.path on emulated NVDIMMs
- This method can remain as another method for cross-checking, but interface to the rest of driver should be unified then, which is not in short-term plans

### ndctl CLI interface replaced by Go-language interface to libndctl, using cgo

- Go interface that uses #cgo and linkage to libndctl, replaces ndctl CLI layer
- This code resides in branch:gondctl/devel
- Uses full power of libndctl
- Combined with LVM to solve fragmentation problem

### Semantics

Some sane, data-preserving semantics rules need to be agreed on. For example, some scenarios:
  - Server reboots and driver starts again. The namespaces with contents are expected to be preserved, but mounts are gone, and mount points may exist or may be gone as well. Driver should work so that requests by volume names that have existed before, do not destroy/recreate file systems but try to reach the same state that was active before reboot.

## State

The code builds, runs as standalone, see `run_driver`
and responds to API use on TCP or Unix socket.

# Development system configuration

So far, the development and verification has mostly been carried out on qemu-emulated NVDIMMs, with brief testing on a system having real 2x256 GB of persistent memory

Build has been verified on a system described below.

Running the steps forming a simple volume lifecycle has been verified using csc on same system. See steps using 'csc' in `run_client_csc`.

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

This produces driver binary `./_output/pmem-csi-driver`

## Docker-based build

`make pmem-csi-driver-container`

This succeeds and produces container image and driver can be
started inside of that image:

`docker run IMAGE --endpoint tcp://127.0.0.1:10000`

Further container-based use scenarios have not been explored yet.

# Test

`make test`

This does not succeed yet

# Run

1. start driver (as user:root) as in `run_driver`
2. Run client-side commands (as regular user) as in `run_client_csc`

The client-side endpoint can be specified either:

- with each csc command as `--endpoint tcp://127.0.0.1:10000` 
- export endpoint as env.variable, see `run_client_csc`
