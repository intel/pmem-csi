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
- [Notes about switching DeviceMode](#notes-about-switching-devicemode)
    - [Going from DeviceMode:LVM to DeviceMode:Direct](#going-from-devicemodelvm-to-devicemodedirect)
    - [Going from DeviceMode:Direct to DeviceMode:LVM](#going-from-devicemodedirect-to-devicemodelvm)
- [Notes about accessing system directories in a container](#notes-about-accessing-system-directories-in-a-container)
    - [Read-only access to /sys](#read-only-access-to-sys)
    - [Access to /dev of host](#access-to-dev-of-host)
-   [Repository elements which are generated or created separately](#repository-elements-which-are-generated-or-created-separately)
    -   [Top-level README diagrams describing LVM and Direct device modes](#top-level-readme-diagrams-describing-lvm-and-direct-device-modes)
    -   [Top-level README diagram describing communication channels](#top-level-readme-diagram-describing-communication-channels)
    -   [Diagrams describing provisioning sequence](#diagrams-describing-provisioning-sequence)
    -   [RegistryServer spec](#registryserver-spec)
    -   [Table of Contents in README and DEVELOPMENT](#table-of-contents-in-readme-and-development)

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

- registry endpoint: typical port value 10000, used for PMEM-CSI internal communication
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
-mode string               | driver run mode                                | string | controller, node |
-nodeid string             | node id                                        | string |              | nodeid
-registryEndpoint string   | endpoint to connect/listen registry server     | string |              |


Environment variables
---------------------

TEST_WORK is used by registry server unit-test code to specify path to certificates in test system. 
Note, THIS IS NOT USED IN PRODUCTION

Notes about switching DeviceMode
================================

If DeviceMode is switched between LVM and Direct(ndctl), please keep
in mind that PMEM-CSI driver does not clean up or reclaim Namespaces,
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

If PMEM-CSI driver has been operating correctly, there should not be
existing Namespaces as CSI Volume lifecycle should have been deleted
those after end of life of Volume. If there are, you can either keep
those (DeviceMode:LVM does honor "foreign" Namespaces and leaves those
alone) if you have enough space, or you can choose to delete those
using `ndctl` on node.

Notes about accessing system directories in a container
=======================================================

The PMEM-CSI driver will run as container, but it needs access to
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

Repository elements which are generated or created separately
=============================================================

Here are creation and update notes for these elements in the repository
which are not hand-edited

Top-level README diagrams describing LVM and Direct device modes
----------------------------------------------------------------
Two diagrams are created with [_dia_ drawing program](https://en.wikipedia.org/wiki/Dia_(software)).
The [single source file](/docs/diagrams/pmem-csi.dia) has
layers: {common, lvm, direct} so that two diagram variants can be produced from single source.
Image files are produced by saving in PNG format with correct set of layers visible.
The PNG files are committed as repository elements in docs/images/devicemodes/.

Top-level README diagram describing communication channels
----------------------------------------------------------
This diagram was created with the [_dia_ drawing program](https://en.wikipedia.org/wiki/Dia_(software)) using [source file](/docs/diagrams/pmem-csi-communication-diagram.dia).

Image file is produced by saving in PNG format.
The PNG file is committed as a [repository element](/docs/images/communication/pmem-csi-communication-diagram.png).

Diagrams describing provisioning sequence
-----------------------------------------
Two diagrams are generated using [plantuml program](http://plantuml.com/).
Source files:
- [source file for regular sequence](/docs/diagrams/sequence.wsd)
- [source file for cache sequence](/docs/diagrams/sequence-cache.wsd)

The PNG files are committed as repository elements in docs/images/sequence/.

RegistryServer spec
-------------------
pkg/pmem-registry/pmem-registry.pb.go is generated from pkg/pmem-registry/pmem-registry.proto

protoc comes from package _protobuf-compiler_ on Ubuntu 18.04
- get protobuf for Go:
```sh
$ git clone https://github.com/golang/protobuf.git && cd protobuf
$ make # installs needed binary in $GOPATH/bin/protoc-gen-go
```

- generate by running in ~/go/src/github.com/intel/pmem-csi/pkg/pmem-registry:
```sh
protoc --plugin=protoc-gen-go=$GOPATH/bin/protoc-gen-go --go_out=plugins=grpc:./ pmem-registry.proto
```

Table of Contents in README and DEVELOPMENT
-------------------------------------------
Table of Contents can be generated using multiple methods.
- One possibility is to use [pandoc](https://pandoc.org/)

```sh
$ pandoc -s -t markdown_github --toc README.md -o /tmp/temp.md
```

Then check and hand-pick generated TOC part(s) from /tmp/temp.md and insert in desired location.
Note that pandoc is known to produce incorrect TOC entries if headers contain special characters,
means TOC generation will be more reliable if we avoid non-letter-or-number characters in the headers.

- Another method is to use emacs package markdown-toc-generate-toc
