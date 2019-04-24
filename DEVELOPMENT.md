Table of Contents
----------------

- [Code quality](#code-quality)
    - [Coding style](#coding-style)
    - [Input validation](#input-validation)
- [APIs](#apis)
    - [CSI API](#csi-api)
    - [Network ports](#network-ports)
    - [Local sockets](#local-sockets)
    - [Command line arguments](#command-line-arguments)
    - [Common arguments](#common-arguments)
    - [Specific arguments to pmem-ns-init](#specific-arguments-to-pmem-ns-init)
    - [Specific arguments to pmem-vgm](#specific-arguments-to-pmem-vgm)
    - [Specific arguments to pmem-csi-driver](#specific-arguments-to-pmem-csi-driver)
    - [Environment variables](#environment-variables)
- [Development mode build and run](#development-mode-build-and-run)
    - [Development system using Virtual Machine with emulated devices](#development-system-using-virtual-machine-with-emulated-devices)
    - [Build plugin for stand-alone use](#build-plugin-for-stand-alone-use)
    - [Stand-alone mode build potential issues](#stand-alone-mode-build-potential-issues)
    - [Run driver as stand-alone program](#run-driver-as-stand-alone-program)
    - [Verify driver in unified mode](#verify-driver-in-unified-mode)
    - [Scripts in util/ directory](#scripts-in-util-directory)
    - [Override registry address](#override-registry-address)
- [Notes about switching DeviceMode](#notes-about-switching-devicemode)
    - [Going from DeviceMode:LVM to DeviceMode:Direct](#going-from-devicemodelvm-to-devicemodedirect)
    - [Going from DeviceMode:Direct to DeviceMode:LVM](#going-from-devicemodedirect-to-devicemodelvm)
- [Notes about accessing system directories in a container](#notes-about-accessing-system-directories-in-a-container)
    - [Read-only access to /sys](#read-only-access-to-sys)
    - [Access to /dev of host](#access-to-dev-of-host)

Code quality
============

Coding style
------------

The normal Go style guide applies. It is enforced by `make test`, which calls `gofmt`. `gofmt` should be
from Go >= v1.11.4, older version (like v1.10) format the code slightly differently.


Input validation
----------------

In all cases, input comes from a trusted source because network
communication is protected by mutual TLS and the `kubectl` binaries
runs with the same privileges as the user invoking it.

Nonetheless, input needs to be validated to catch mistakes:

- detect incorrect parameters for `kubectl`
- ensure that messages passed to gRPC API implementations
  in registry, controller and driver have all required
  fields
- the gRPC implementation rejects incoming messages that are too large
  (https://godoc.org/google.golang.org/grpc#MaxRecvMsgSize) and
  refuses to send messages that are larger
  (https://godoc.org/google.golang.org/grpc#MaxSendMsgSize)


APIs
============

CSI API
----------------

Kubernetes CSI API is exposed over Unix domain socket. CSI operations
are executed as gRPC calls. Input data is allowed as permitted by CSI
specification. Output data is formatted as gRPC response.

Following CSI operations are supported, with arguments as specified by
CSI specification: CreateVolume, DeleteVolume, StageVolume,
UnstageVolume, PublishVolume, UnpublishVolume, ListVolumes,
GetCapacity, GetCapabilities, GetPluginInfo, GetPluginCapabilities.


Network ports
-------------

Network ports are opened as configured in manifest files:

- registry endpoint: typical port value 10000, used for pmem-CSI internal communication
- controller endpoint: typical port value 10001, used for serving CSI API


Local sockets
-------------

Kubernetes CSI API used over local socket inside same host.

- unix:///var/lib/kubelet/plugins/pmem-csi-reg.sock
- unix:///var/lib/kubelet/plugins/pmem-csi/csi.sock
- unix:///var/lib/kubelet/plugins/pmem-csi/csi-controller.sock


Command line arguments
----------------------

Note that different set of programs is used in different
DeviceModes. Three stages: *pmem-ns-init*, *pmem-vgm*,
*pmem-csi-driver* run in DeviceMode:LVM. Only *pmem-csi-driver* runs
in DeviceMode:Direct.

Common arguments
----------------

argument name            | meaning                                           | type   | range
-------------------------|---------------------------------------------------|--------|---
-alsologtostderr         | log to standard error as well as files            |        |
-log_backtrace_at value  | when logging hits line file:N, emit a stack trace |        |
-log_dir string          | If non-empty, write log files in this directory   | string |
-log_file string         | If non-empty, use this log file                   | string |
-logtostderr             | log to standard error instead of files            |        |
-skip_headers            | avoid header prefixes in the log messages         |        |
-stderrthreshold value   | logs at or above this threshold go to stderr (default 2) | |
-v value                 | log level for V logs                              | int    |
-vmodule value           | comma-separated list of pattern=N settings for file-filtered logging | string |

Specific arguments to pmem-ns-init
-----------------------------------

argument name      | meaning                                                | type | range
-------------------|--------------------------------------------------------|------|---
-namespacesize in  | Namespace size in GB (default 32)                      | int  | set to 2 if less than 2
-useforfsdax int   | Percentage of total to use in Fsdax mode (default 100) | int  | 0..100
-useforsector int  | Percentage of total to use in Sector mode (default 0)  | int  | 0..100

Note: useforfsdax + useforsector must be <=100


Specific arguments to pmem-vgm
-------------------------------

NO SPECIFIC arguments

Specific arguments to pmem-csi-driver
--------------------------------------

argument name        | meaning                                           | type | allowed | default
---------------------|---------------------------------------------------|------|---------|---
-caFile string       | Root CA certificate file to use for verifying connections | string | |
-certFile string     | SSL certificate file to use for authenticating client connections(RegistryServer/NodeControllerServer) | string | |
-clientCertFile string | Client SSL certificate file to use for authenticating peer connections | string | | certFile
-clientKeyFile string | Client private key associated to client certificate | string |              | keyFile
-controllerEndpoint string | internal node controller endpoint              | string |              |
-deviceManager string      | device manager to use to manage pmem devices   | string | lvm or ndctl | lvm
-drivername string         | name of the driver                             | string |              | pmem-csi
-endpoint string           | PMEM CSI endpoint                              | string |              | unix:///tmp/pmem-csi.sock
-keyFile string            | Private key file associated to certificate     | string |              |
-mode string               | driver run mode                                | string | controller, node, unified | unified
-nodeid string             | node id                                        | string |              | nodeid
-registryEndpoint string   | endpoint to connect/listen registry server     | string |              |


Environment variables
---------------------

TEST_WORK is used by registry server unit-test code to specify path to certificates in test system. 
Note, THIS IS NOT USED IN PRODUCTION

Development mode build and run
==============================

It is possible to build and try the plugin on a stand-alone system without Kubernetes involved. This may be useful in the development mode. The plugin can be started in Unified mode where both Controller and Node functions are served by a single process running on one host.

Development system using Virtual Machine with emulated devices
--------------------------------------------------------------

Development and some verification can be performed using QEMU-emulated persistent memory devices instead of HW-based persistent memory devices. See [README-qemu-notes](README-qemu-notes.md) for more details about VM configuration.


Build plugin for stand-alone use
--------------------------------

Building locally for stand-alone use has been verified using these components:

- Go: versions 1.10, 1.11 are usable for building, go 1.11 is required for clean run of 'make test`
- [ndctl](https://github.com/pmem/ndctl) versions 62..64, either built on dev.host via autogen, configure, make, and installed using instructions in README.md, or installed as ndctl package(s) from distribution repository.

Use `make`

This produces the following binaries in the `_output` directory:

  * `pmem-ns-init`: Utility for namespace initialization in DeviceMode:LVM
  * `pmem-vgm`: Utility for creating logical volume groups over PMEM devices created, used in DeviceMode:LVM
  * `pmem-csi-driver`: pmem-CSI driver, used in both DeviceModes


Stand-alone mode build potential issues
---------------------------------------

In a recent case study it was observed that stand-alone mode build may fail:

- Ubuntu 18.04
- Go 1.11.5 installed into /usr/local using available-on-net instructions
  (note that distro-supported Go is 1.10.4 and gets installed in /usr/lib, and will not expose this issue)
- libndctl v64.1 built manually using `./configure` and installed, with shared libs end up in /usr/lib/
- note that distro-supported libndctl would be v61

On such a system, stand-alone mode make will fail like this:
```
GOOS=linux go build -a -o _output/pmem-csi-driver ./cmd/pmem-csi-driver # github.com/intel/pmem-csi/pkg/ndctl
/tmp/go-build681207083/b159/_x007.o: In function `_cgo_0eba3381006a_Cfunc_ndctl_region_get_max_available_extent':
/tmp/go-build/cgo-gcc-prolog:264: undefined reference to `ndctl_region_get_max_available_extent'
collect2: error: ld returned 1 exit status
Makefile:52: recipe for target 'pmem-csi-driver' failed
```

The problem seems to be that CGo linker looks for shared libraries in GOROOT-related directories, and does not consider /usr/lib.
One work-around is to prepend linker flags to compile command by replacing line in Makefile:

`GOOS=linux go build -a -o _output/$@ ./cmd/$@`

with line:

`CGO_LDFLAGS=-L/usr/lib GOOS=linux go build -a -o _output/$@ ./cmd/$@`

This example demonstrates that both Go environment and libndctl shared libraries may be built and/or installed manually on various paths, bypassing package management, as newer-than-distro-provided versions are sometimes desired. It is not straightforward to support all different combinations without creating complexity-adding dynamic detection of components, but that is out of scope currently, so some local work-arounds may be needed.

Run driver as stand-alone program
---------------------------------

Use `util/run-lvm-unified` as user:root to start the driver in DeviceMode:LVM.
This runs two preparation parts and starts the driver binary, which listens and responds to API use on a TCP socket. You can modify this to use a Unix domain socket, if needed.
Use `util/run-direct-unified` as user:root to start the driver in DeviceMode:direct. In this mode the two preparation stages are skipped.

Verify driver in unified mode
-----------------------------

Some of the functionality of the driver can be verified in the single-host context. This running mode is called "Unified" in the driver. Both Controller and Node service run combined in local host, without Kubernetes context.

The endpoint for driver access can be specified either:

* with each csc command as `--endpoint tcp://127.0.0.1:10000`
* export endpoint as env.variable, see `util/lifecycle-unified.sh`

These run-time dependencies are used by the plugin in the Unified mode: lvm2 shred mount file blkid

Scripts in util/ directory
--------------------------

* [lifecycle-unified](util/lifecycle-unified.sh) example steps verifying a volume lifecycle
* [sanity-unified](util/sanity-unified.sh) API test using csi-sanity
* [get-capabilities-unified](util/get-capabilities-unified.sh) Query Controller and Node capabilities

These utilities are required by scripts residing in `util/` directory:

- [csc](http://github.com/rexray/gocsi)
- [csi-sanity](http://github.com/kubernetes-csi/csi-test)

Override registry address
-------------------------

Sometimes images need to be deployed from specific registry instead of
what is written in deployment files. You can use this example command
to change registry address in all manifest files under deploy/
directory:

```
for f in $(find ./deploy -type f); do sed -i -e 's/192.168.8.1:5000/MY_REGISTRY/' $f; done
```

Notes about switching DeviceMode
================================

If DeviceMode is switched between LVM and Direct(ndctl), please keep
in mind that pmem-CSI driver does not clean up or reclaim Namespaces,
therefore Namespaces plus other related context (possibly LVM state)
created in previous mode will remain stored on device and most likely
will create trouble in another DeviceMode.

Going from DeviceMode:LVM to DeviceMode:Direct
----------------------------------------------

- examine LV Groups state on a node: `vgs`
- examine LV Phys.Volumes state on a node: `pvs`
- Delete LV Groups before deleting namespaces: `vgremove VGNAME`, to
  avoid orphaned VGroups

NOTE: The next **WILL DELETE ALL NAMESPACES** so be careful!

- Delete Namespaces on a node using CLI: `ndctl destroy-namespace all --force`

Going from DeviceMode:Direct to DeviceMode:LVM
----------------------------------------------

No special steps are needed to clean up Namespaces state.

If pmem-CSI driver has been operating correctly, there should not be
existing Namespaces as CSI Volume lifecycle should have been deleted
those after end of life of Volume. If there are, you can either keep
those (DeviceMode:LVM does honor "foreign" Namespaces and leaves those
alone) if you have enough space, or you can choose to delete those
using `ndctl` on node.

Notes about accessing system directories in a container
=======================================================

The pmem-CSI driver will run as container, but it needs access to
system directories /sys and /dev. Two related potential problems have
been diagnosed so far.

Read-only access to /sys
------------------------

In some deployment schemes /sys remains mounted read-only in the
container running pmsm-csi-driver. This is not problem in
DeviceMode:LVM, but is a blocking problem in DeviceMode:Direct where
the driver needs write access to /sys for Namespaces management
operations. There is start-time check for read-write mount of /sys in
the code. An error in pod log `pmem-driver: Failed to run driver:
FATAL: /sys mounted read-only, can not operate` is the sign of such
state.

Access to /dev of host
----------------------

Containers runtime may not pass /dev from host into the
container. This is, again, problem in DeviceMode:Direct. If the /dev/
of the host is not accessible in the PMEM-CSI container, there will be
failure in accessing of newly created block device /dev/pmemX.Y which
will not be visible inside container. The driver does not detect the
root cause of that problem during start-up, but only when a volume
creation has failed. This problem can be avoided by specifying
explicit mount of /dev in the PMEM-CSI manifest.
