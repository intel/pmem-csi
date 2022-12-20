# Introduction to PMEM-CSI for Kubernetes

**Note: This is Alpha code and not production ready.**

Intel PMEM-CSI is a [CSI](https://github.com/container-storage-interface/spec)
storage driver for container orchestrators like
Kubernetes. It makes local persistent memory
([PMEM](https://pmem.io/)) available as a filesystem volume to
container applications. It can currently utilize non-volatile memory
devices that can be controlled via the [libndctl utility
library](https://github.com/pmem/ndctl). In this readme, we use
*persistent memory* to refer to a non-volatile dual in-line memory
module (NVDIMM).

The [v1.1 release](https://github.com/intel/pmem-csi/releases/latest)
is the latest feature release and is [regularly updated](docs/DEVELOPMENT.md#release-management) with newer base images
and bug fixes. Older releases are no longer supported.

Documentation is part of the source code for each release and also
available in rendered form for easier reading:
- [latest 1.1.x release](https://intel.github.io/pmem-csi/1.1/)
- [latest 1.0.x release](https://intel.github.io/pmem-csi/1.0/)
- [latest 0.9.x release](https://intel.github.io/pmem-csi/0.9/)
- [latest 0.8.x release](https://intel.github.io/pmem-csi/0.8/)
- [latest 0.7.x release](https://intel.github.io/pmem-csi/0.7/)
- [latest documentation, in development](https://intel.github.io/pmem-csi/devel/)

## Supported Kubernetes versions

PMEM-CSI implements the CSI specification version 1.x, which is only
supported by Kubernetes versions >= v1.13. The following table
summarizes the status of support for PMEM-CSI on different Kubernetes
versions:

| Kubernetes version | Required alpha feature gates   | Support status
|--------------------|--------------------------------|----------------
| 1.13               | CSINodeInfo, CSIDriverRegistry,<br>CSIBlockVolume</br>| unsupported <sup>1</sup>
| 1.14               |                                | unsupported <sup>2</sup>
| 1.15               | CSIInlineVolume                |
| 1.16               |                                |
| 1.17               |                                |
| 1.18               |                                |

<sup>1</sup> Several relevant features are only available in alpha
quality in Kubernetes 1.13 and the combination of skip attach and
block volumes is completely broken, with [the
fix](https://github.com/kubernetes/kubernetes/pull/79920) only being
available in later versions. The external-provisioner v1.0.1 for
Kubernetes 1.13 lacks the `--strict-topology` flag and therefore late
binding is unreliable. It's also a release that is not supported
officially by upstream anymore.

<sup>2</sup> Lacks support for ephemeral inline volumes.
Not supported officially by upstream anymore.

## Demo

Click the image to watch the animated demo on asciinema.org:

[![asciicast](https://asciinema.org/a/4M5PSwbYkaXs0dPHYUIVbakqB.png)](https://asciinema.org/a/4M5PSwbYkaXs0dPHYUIVbakqB)

## License

All of the source code required to build PMEM-CSI is available under
Open Source licenses.  The source code files identify external Go
modules used. Binaries are distributed as container images on
DockerHub. Those images contain license texts under
`/usr/local/share/package-licenses` and source code under
`/usr/local/share/package-sources`.

## Content

- [PMEM-CSI for Kubernetes](#pmem-csi-for-kubernetes)
    - [Supported Kubernetes versions](#supported-kubernetes-versions)
    - [Design and architecture](docs/design.md)
    - [Installation and Usage](docs/install.md)
       - [Prerequisites](docs/install.md#prerequisites)
       - [Installation and setup](docs/install.md#installation-and-setup)
       - [Filing issues and contributing](docs/install.md#filing-issues-and-contributing)
    - [Develop and contribute](docs/DEVELOPMENT.md)
    - [Automated testing](docs/autotest.md)
    - [Application examples](examples/readme.rst)
