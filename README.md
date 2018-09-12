# PMEM CSI Driver

This is a draft version of the PMEM CSI driver.
Code is based on early OIM-CSI driver which in turn
was mostly based on the hostpath driver.

Current down-interface is ndctl commands.

Some early design ideas, some of which may need changing:

- Driver does not maintain state, i.e. state of objects is looked up using down-interface,
which is currently ndctl.
- The "name" field used by ndctl is used as primary key and VolumeID.
- The "name" field value for a new namespace originates from csc CLI commands.
- Mount point path names originate from csc CLI commands.

```
ndctl create-namespace -s 29360128 -n nspace4
{
  "dev":"namespace0.4",
  "mode":"fsdax",
  "map":"dev",
  "size":"24.00 MiB (25.17 MB)",
  "uuid":"40fd7022-134f-48a5-8a70-3ea6222e34d7",
  "raw_uuid":"d90be3c8-1f40-4e00-89fb-67332b4e3325",
  "sector_size":512,
  "blockdev":"pmem0.4",
  "name":"nspace4",       <--------------------- key used as VolumeID
  "numa_node":0
}
```

For operations on devices, driver looks up namespace entry by walking
all namespace entries reported by ndctl, then uses
"dev", "size", "blockdev" values for actual operations.

Current code likely contains leftovers, unused parts, things to fix and clean up.
There are some TODOs marked in the code, and some more are needed.

The code builds, runs as standalone (see `run_driver`)
and responds to API use on TCP or Unix socket.

# Development system config

Build has been verified on a system described below.

Running the steps forming a simple volume lifecycle (see steps using 'csc' in `run_csc_client`) has been verified on same system.

- Host: Dell Poweredge R620, distro: openSUSE Tumbleweed, kernel 4.18.5, qemu 2.12.1
- Guest: VM: 8G RAM, 8 vCPUs, Ubuntu 18.04.1 server, kernel 4.15.0
- VM config originally created by libvirt, with these additions to emulate 512 MB NVDIMM backed by host file:


## maxMemory

```
  <maxMemory slots='16' unit='KiB'>16777216</maxMemory>
```

## numa config

```
  <cpu ...>
    <...>
    <numa>
      <cell id='0' cpus='0-7' memory='8388608' unit='KiB'/>
    </numa>
  </cpu>
```


## emulated NVDIMM with labels support

```
    <memory model='nvdimm' access='shared'>
      <source>
        <path>/var/lib/libvirt/images/nvdimm.0</path>
      </source>
      <target>
        <size unit='KiB'>524288</size>
        <node>0</node>
        <label>
          <size unit='KiB'>2048</size>
        </label>
      </target>
      <address type='dimm' slot='0'/>
    </memory>
```



Software on the guest:
- Go: version 1.10.1
- ndctl: version 62
- csc: built from github.com/rexray/gocsi 0.5.0
- Docker-ce: version 18.06.1

vendor/ directory has been updated on 2018-09-11

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

TODO: this does not succeed yet

# Run

1. start driver (as root) as in `run_driver`
2. Run client-side commands (as user) as in `run_csc_client`

The client-side endpoint can be specified either:

- with each csc command as `--endpoint tcp://127.0.0.1:10000` 
- export endpoint as env.variable, see `run_csc_client`

# Questions, limitations, issues

We should turn (some of) those into issue entries in repository after code check-in

## Sizes delta issue

Seems because of space used by labels (?), the resulting namespace size is smaller than request by 4 * 1024 * 1024 bytes

Example: Request is 32MB, actual namespace size will be 32-4=28 MB

How to handle next request with same size?

Currently, a next request with same size responds:

`Volume with the same name: nspace1 but with different size already exist`

While user expectation is:

Next request with same size should return same namespace as from user POV same thing is asked.

How to solve: Should we compensate the diff internally, i.e. always create REQUEST+4M space?
That leads to state that one can't create as large namespace as the largest free area.


## Single region limitation

Current early-draft driver code operates without regions knowledge, assuming single region.
Driver does not specify any region options to it's down-interface.
ndctl can display multiple regions (if multiple DIMMs are emulated).
TODO: Try multi-regions in qemu-based dev.model by specifying multiple emulated NVDIMMs.

## Numa aspects

NUMA-locality should be taken into account, for example when
selecting region, the NUMA-local will provide the best performance.
Also, a namespace should not span to different NUMA nodes,
or perhaps even safer, not to span to different regions at all?

TODO: try to use multi-numa-node setup in qemu-based version

## Missing namespaces case

if there is no namespaces yet (first use):
"ndctl list --namespaces" returns empty,
but the 'run' function in PMEM CSI driver expects json output which it tries to parse.
This causes following error returned to `~/go/bin/csc controller list-volumes`:
```
E0912 13:15:22.196783   31982 ndctl.go:159] run: unexpected end of JSON input: []
E0912 13:15:22.196802   31982 tracing.go:33] GRPC error: unexpected end of JSON input: []
```

The caller of run() could avoid parsing by omitting interface in args,
but caller does not know if the response will be empty or contain JSON.

## Fragmentation

The space will get fragmented. Driver should be prepared to handle this.

NVDIMM Namespace spec 
https://pmem.io/documents/NVDIMM_Namespace_Spec.pdf

mentions on page 16 possibility to combine multiple ranges of DPA  into one namespace (means fragmentation risk was known),
but ndctl seems to have no such method supported.

Should look into: pmempool, libpmempool

## Data privacy

Should the driver erase data after volume/namespace gets to end-of-life?
If yes, where should this happen, in unstage or delete-volume ?
What about performance implication?
Is there efficient ways to perform erase/wipe on device?
Idea: To make visible erase penalty smaller, do erase on async fashion in background?
(But that risks to lead into more complex cases and possible failure, race etc. scenarios)

## Kernel BUG, qemu-based use

With multiple namespaces, and multiple deletions, kernel BUG happens:
```
[  583.026321] Call Trace:
[  583.026345]  devm_memremap_pages_release+0x10f/0x240
[  583.026374]  release_nodes+0x110/0x1f0
[  583.026396]  devres_release_all+0x3c/0x50
[  583.026422]  device_release_driver_internal+0x173/0x220
```
