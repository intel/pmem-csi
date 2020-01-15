Table of Contents
----------------

- [Setup](#setup)
    - [Build PMEM-CSI](#build-pmem-csi)
- [Code quality](#code-quality)
    - [Coding style](#coding-style)
    - [Input validation](#input-validation)
- [Release management](#release-management)
    - [Branching](#branching)
    - [Tagging](#tagging)
    - [Release checklist](#release-checklist)
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
- [Notes about switching device mode](#notes-about-switching-device-mode)
    - [Going from LVM device mode to direct device mode](#going-from-lvm-device-mode-to-direct-device-mode)
    - [Going from direct device mode to LVM device mode](#going-from-direct-device-mode-to-lvm-device-mode)
- [Notes about accessing system directories in a container](#notes-about-accessing-system-directories-in-a-container)
    - [Read-only access to /sys](#read-only-access-to-sys)
    - [Access to /dev of host](#access-to-dev-of-host)
- [Repository elements which are generated or created separately](#repository-elements-which-are-generated-or-created-separately)
    - [Top-level README diagrams describing LVM and Direct device modes](#top-level-readme-diagrams-describing-lvm-and-direct-device-modes)
    - [Top-level README diagram describing communication channels](#top-level-readme-diagram-describing-communication-channels)
    - [Diagrams describing provisioning sequence](#diagrams-describing-provisioning-sequence)
    - [RegistryServer spec](#registryserver-spec)
    - [Table of Contents in README and DEVELOPMENT](#table-of-contents-in-readme-and-development)

Setup
============

Build PMEM-CSI
--------------

1.  Use `make build-images` to produce Docker container images.

2.  Use `make push-images` to push Docker container images to a Docker image registry. The
    default is to push to a local [Docker registry](https://docs.docker.com/registry/deploying/).
    Some other registry can be configured by setting the variables described in
    in the [test-config.sh](test/test-config.sh) file, see the [configuration options](#configuration-options)
    section below. Alternatively, the registry can also be set with a make variable:
    `make push-images REGISTRY_NAME=my-registry:5000`

See the [Makefile](Makefile) for additional make targets and possible make variables.

The source code gets developed and tested using the version of Go that
is set with `GO_VERSION` in the [Dockerfile](Dockerfile). Some other
version may or may not work. In particular, `test_fmt` and
`test_vendor` are known to be sensitive to the version of Go.

Code quality
============

Coding style
------------

The normal Go style guide applies. It is enforced by `make test`, which calls `gofmt`.


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

Release management
==================

Branching
---------

The `master` branch is the main branch. It is guaranteed to have
passed full CI testing. However, it always uses the latest Clear Linux
for building container images, so changes in Clear Linux can break the
building of older revisions.

The `devel` branch contains additional commits on top of `master`
which might not have been tested in that combination yet. Therefore it
may be a bit less stable than `master`. The `master` branch gets
advanced via a fast-forward merge after successful testing by the CI job
that rebuilds and tests the `devel` branch.

Code changes are made via pull requests against `devel`. Each of them
will get tested separately by the CI system before merging, but only a
subset of the tests can be run due to time constraints.

Beware that after merging one PR, the existing pre-merge tests results
for other PRs become stale because they were based on the old `devel`
branch. Because `devel` is allowed to be less stable than `master`, it
is okay to merge two PRs quickly after one another without
retesting. If two PRs that merged that don't have code conflicts
(which would get detected by GitHub) but which nonetheless don't work
together, the combined testing in the `devel` branch will find
that. This will block updating `master` and thus needs to be dealt
quickly.

Releases are created by branching `release-x.y` from `master` or some
older, stable revision. On that new branch, the base image is locked
onto a certain Clear Linux version with the
`hack/update-clear-linux-base.sh` script. Those `release-x.y` branches
are then fully reproducible. The actual `vx.y.z` release tags are set
on revisions in the corresponding `release-x.y` branch.

Releases and the corresponding images are never changed. If something
goes wrong after setting a tag (like detecting a bug while testing the
release images), a new release is created.

Container images reference a fixed base image. To ensure that the base
image remains secure, `hack/update-clear-linux-base.sh` gets run
periodically to update a `release-x.y` branch and a new release with
`z` increased by one is created. Other bug fixed might be added to
that release by merging into the branch.

Tagging
-------

The `devel` and `master` branch build and use the `canary` version of
the PMEM-CSI driver images. Before tagging a release, all of those
version strings need to be replaced by the upcoming version. All
tagged releases then use the image that corresponds to that release.

The `hack/set-version.sh` script can be used to set these versions.
The modified files then need to be committed. Merging such a commit
triggers a rebuild of the `devel` branch, but does not yet produce a
release: the actual image only gets pushed when there is a tag that
corresponds to the version embedded in the source code. The
Jenkinsfile ensures that.

Release checklist
-----------------

* Create a new `release-x.y` branch.
* Run `hack/update-clear-linux-base.sh`.
* Run `hack/set-version.sh vx.y.z` and commit the modified files.
* Push to `origin`.
* [Create a draft
  release](https://github.com/intel/pmem-csi/releases/new) for that
  new branch, including a change log gathered from new commits.
* Review the change log.
* Tag `vx.y.z` manually and push to origin.
* Wait for a successful CI [build for that
  tag](https://cloudnative-k8sci.southcentralus.cloudapp.azure.com/view/pmem-csi/job/pmem-csi/view/tags/)
  and promotion of the resulting images to [Docker
  Hub](https://hub.docker.com/r/intel/pmem-csi-driver/tags?page=1&ordering=last_updated).
* Publish the GitHub release.

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
device modes. Three stages: *pmem-ns-init*, *pmem-vgm*,
*pmem-csi-driver* run in LVM device mode. Only *pmem-csi-driver* runs
in direct device mode.

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
-useforfsdax int   | Percentage of total to use in Fsdax mode (default 100) | int  | 0..100

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
-deviceManager string      | device mode to use. ndctl selects mode which is described as direct mode in documentation. | string | lvm or ndctl | lvm
-drivername string         | name of the driver                             | string |              | pmem-csi
-endpoint string           | PMEM CSI endpoint                              | string |              | unix:///tmp/pmem-csi.sock
-keyFile string            | Private key file associated to certificate     | string |              |
-mode string               | driver run mode                                | string | controller, node |
-nodeid string             | node id                                        | string |              | nodeid
-registryEndpoint string   | endpoint to connect/listen registry server     | string |              |
-statePath                 | Directory path where to persist the state of the driver running on a node | string | absolute directory path on node | /var/lib/<drivername>

Environment variables
---------------------

TEST_WORK is used by registry server unit-test code to specify path to certificates in test system. 
Note, THIS IS NOT USED IN PRODUCTION

Notes about switching device mode
================================

If device mode is switched between LVM and direct(aka ndctl), please keep
in mind that PMEM-CSI driver does not clean up or reclaim namespaces,
therefore namespaces plus other related context (LVM state)
created in previous mode will remain stored on device and most likely
will create trouble in another device mode.

Going from LVM device mode to direct device mode
------------------------------------------------

- examine LV groups state on a node: `vgs`
- examine LV physical volumes state on a node: `pvs`
- delete LV groups before deleting namespaces to avoid orphaned volume groups: `vgremove VGNAME`

NOTE: The next **WILL DELETE ALL NAMESPACES** so be careful!

- Delete namespaces on a node using CLI: `ndctl destroy-namespace all --force`

Going from direct device mode to LVM device mode
----------------------------------------------

No special steps are needed to clean up namespaces state.

If PMEM-CSI driver has been operating correctly, there should not be
existing namespaces as CSI volume lifecycle should have been deleted
those after end of life of volume. If there are, you can either keep
those (LVM device mode does honor "foreign" namespaces and leaves those
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
container running pmsm-csi-driver. This creates problem for the
driver which needs write access to /sys for namespaces management
operations. There is start-time check for read-write mount of /sys in
the code. An error in pod log `pmem-driver: Failed to run driver:
FATAL: /sys mounted read-only, can not operate` is the sign of such
state.

Access to /dev of host
----------------------

Containers runtime may not pass /dev from host into the
container. If the /dev/ of the host is not accessible in the PMEM-CSI container, there will be
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

- Another method is to use emacs command markdown-toc-generate-toc and manually check and edit the generated part: we do not show generated 3rd-level headings in README.md.
