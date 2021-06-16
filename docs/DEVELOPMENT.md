# Develop and Contribute

## Setup

### Build PMEM-CSI

1.  Use `make build-images` to produce Docker\* container images.

2.  Use `make push-images` to push Docker container images to a Docker image
    registry. The default is to push to a local
    [Docker registry](https://docs.docker.com/registry/deploying/).
    Other registries can be configured by setting the variables
    described in the [test-config.sh](/test/test-config.sh) file. See the
    [configuration options](autotest.md#configuration-options)
    section below. Alternatively, the registry can also be set with a make
    variable:
    `make push-images REGISTRY_NAME=my-registry:5000`

See the [Makefile](/Makefile) for additional make targets and possible make variables.

The source code is developed and tested using the version of Go that
is set with `GO_VERSION` in the [Dockerfile](/Dockerfile). Other
versions may or may not work. In particular, `test_fmt` and
`test_vendor` are known to be sensitive to the version of Go.

## Code quality

### Coding style

The normal Go style guide applies and is enforced by `make test`, which
calls `gofmt`.


### Input validation

In most cases, input comes from a trusted source because network
communication is protected by mutual TLS. The `kubectl` binaries
run with the same privileges as the user invoking it.

Nonetheless, input needs to be validated to catch mistakes:

- Detect incorrect parameters for `kubectl`
- Ensure that messages passed to gRPC API implementations
  in registry, controller, and driver have all required fields
- The gRPC implementation rejects incoming messages that are too large
  (https://godoc.org/google.golang.org/grpc#MaxRecvMsgSize) and
  refuses to send messages that are larger
  (https://godoc.org/google.golang.org/grpc#MaxSendMsgSize)
- Webhook and metrics SDK code validates input before invoking PMEM-CSI

## Release management

### Branching

The `master` branch is the main branch. It is guaranteed to have
passed full CI testing. However, the Dockerfile uses whatever is
the latest upstream content for the base distribution and therefore
tests results are not perfectly reproducible.

The `devel` branch contains additional commits on top of `master`
which might not have been tested in that combination yet. Therefore it
may be a bit less stable than `master`. The `master` branch gets
advanced via a fast-forward merge after successful testing by the CI job
that rebuilds and tests the `devel` branch.

Code changes are made via pull requests against `devel`. Each of them
will be tested separately by the CI system before merging, but only a
subset of the tests can be run due to time constraints.

Beware that after merging one PR, the existing pre-merge tests results
for other PRs become stale because they were based on the old `devel`
branch. Because `devel` is allowed to be less stable than `master`, it
is okay to merge two PRs quickly after one another without
retesting. If two merged PRs don't have code conflicts
(which would be detected by GitHub\*) but nonetheless don't work
together, the combined testing in the `devel` branch will find
that. This blocks updating `master` and thus needs to be dealt with
quickly.

Releases are created by branching `release-x.y` from `master` or some
older, stable revision. The actual `vx.y.z` release tags are set
on revisions in the corresponding `release-x.y` branch.

Releases and the corresponding images are never changed. If something
goes wrong after setting a tag (such as detecting a bug while testing the
release images), a new release is created.

Container images reference a floating base image. Whatever version of
that was current at the time of building the PMEM-CSI image then
serves as base for the release. To ensure that the release image
remains secure, it is scanned for known vulnerabilities regularly and
a new release is prepared manually, if needed. The new release then
uses a newer base image.

### Tagging

The `devel` and `master` branch build and use the `canary` version of
the PMEM-CSI driver images. Before tagging a release, all of those
version strings need to be replaced by the upcoming version. All
tagged releases then use the image that corresponds to that release.

The `hack/set-version.sh` script can be used to set these versions.
The modified files then need to be committed. Merging such a commit
triggers a rebuild of the `devel` branch, but does not yet produce a
release; the actual image is pushed only when there is a tag that
corresponds to the version embedded in the source code. The
Jenkinsfile ensures that.

### Release checklist

* Create a new `release-x.y` branch.
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
* Run `hack/merge-release.sh` on the "devel" branch and push the
  fabricated merge commit. This documents that "devel" is at least
  as recent as the new release.

### Release PMEM-CSI operator

Follow the steps below to publish new operator release to
[OperatorHub](https://operatorhub.io):

* Generate OLM catalog for new release
``` console
$ make operator-generate-catalog VERSION=<X.Y.Z> #semantic version number
```
Running the above command generates the OLM catalog files under `deploy/olm-catalog/<X.Y.Z>`

* Clone `operator-framework/community-operators` repository
``` console
$ git clone https://github.com/operator-framework/community-operators.git
```

* Copy the generated catalog files. Commit the changes and submit a pull
  request to
 [community-operators](https://github.com/operator-framework/community-operators) repository.
```console
$ cp -r <PMEM-CSI_ROOT>/deploy/olm-catalog/* <COMMUNITY-OPERATORS_ROOT>/upstream-community-operators/pmem-csi-operator/
$ cd <COMMUNITY-OPERATORS_ROOT>
$ git add upstream-community-operators/pmem-csi-operator/
$ git commit -s -m "Updating PMEM-CSI Operator to version <X.Y.Z>"
```

## APIs

### CSI API

Kubernetes\* CSI API is exposed over a Unix domain socket. CSI operations
are executed as gRPC calls. Input data is allowed as permitted by CSI
specification. Output data is formatted as a gRPC response.

The following CSI operations are supported, with arguments specified by
the CSI specification: CreateVolume, DeleteVolume, StageVolume,
UnstageVolume, PublishVolume, UnpublishVolume, ListVolumes,
GetCapacity, GetCapabilities, GetPluginInfo, and GetPluginCapabilities.


### Network ports

Network ports are opened as configured in manifest files:

- metrics endpoint: typical port values 10010 (PMEM-CSI) and 10011
  (external-provisioner)
- webhook endpoint: disabled by default, port chosen when [enabling the scheduler extensions](../README.md#enable-scheduler-extensions)

### Local sockets

The Kubernetes CSI API is used over a local socket inside the same host.

- unix:///var/lib/kubelet/plugins/pmem-csi-reg.sock
- unix:///var/lib/kubelet/plugins/pmem-csi/csi.sock
- unix:///var/lib/kubelet/plugins/pmem-csi/csi-controller.sock


### Command line arguments

See the `main.go` files of the
[pmem-csi-driver](./pkg/pmem-csi-driver/main.go) and
the [pmem-csi-operator](./pkg/pmem-csi-operator/main.go) commands.

### Environment variables

TEST_WORK is used by registry server unit-test code to specify the path to
certificates in the test system.

>Note: THIS IS NOT USED IN PRODUCTION.

NODE_NAME is a copy of the node name set for the pod which runs the
`external-provisioner` on each node.

### Logging

The klog.Info statements are used via the verbosity checker using the following levels:
- klog.V(3) - Generic information. Level 3 is the default Info log level in
  pmem-csi, and example deployment files set this level for production
  configuration.
- klog.V(4) - Elevated verbosity messages.
- klog.V(5) - Even more verbose messages, useful for debugging and issue
  resolving. This level is used in testing type of deployment examples.

These are *not* the same levels as in the [Kubernetes logging conventions](https://github.com/kubernetes/community/blob/4eeba4d57bed01502cb09598a74d21671d4ee876/contributors/devel/sig-instrumentation/logging.md).

There are also messages using klog.Warning, klog.Error, klog.Fatal,
and their formatted counterparts.

## Performance and resource measurements

The [metrics
server](https://github.com/kubernetes-sigs/metrics-server) is needed
for `kubectl top node` and `kubectl top pod`. In the QEMU cluster it
has to be installed with insecure TLS because of
https://github.com/kubernetes/kubeadm/issues/2028. This can be done with:

```console
kustomize build deploy/kustomize/metrics-server | kubectl create -f -
```

The `Vertical Pod Autoscaler` can be used to determine resource
requirements of the PMEM-CSI pods. The `hack/setup-va.sh` script
checks out the source code under `_work` and installs it.

For PMEM-CSI running in the default namespace, VPA can be instructed
to provide recommendations with:

```console
kubectl apply -k deploy/kustomize/vpa-for-pmem-csi/
```

Resource requirements depend on the workload. To generate some load, run
```console
make test_e2e TEST_E2E_FOCUS=lvm-production.*late.binding.*stress.test
```

Alternatively, one can run the
[`hack/stress-driver.sh`](hack/stress-driver.sh)
helper script to generate load on the driver
```console
ROUNDS=500 VOL_COUNT=5 ./hack/stress-driver.sh
```

Now resource recommendations can be retrieved with:

```console
kubectl get vpa
kubectl describe vpa
kubectl get vpa pmem-csi-node -o jsonpath='{range .status.recommendation.containerRecommendations[*]}{.containerName}{":\n\tRequests: "}{.lowerBound}{"\n\tLimits: "}{.upperBound}{"\n"}{end}'
```

The default resource requirements used for the driver deployments by the
operator are chosen from the VPA recommendations described in this section
when using the `stress-driver.sh` script.

## Accessing system directories in a container

The PMEM-CSI driver runs as a container, but it needs access to
system directories /sys and /dev. Two related potential problems that have
been diagnosed so far are listed below.

### Read-only access to /sys

In some deployment schemes, /sys remains mounted read-only in the
container running pmsm-csi-driver. This creates a problem for the
driver that needs write access to /sys for namespaces management
operations. There is start-time check for read-write mount of /sys in
the code. An error in the pod log `pmem-driver: Failed to run driver:
FATAL: /sys mounted read-only, can not operate` is the sign of such a
state.

### Access to /dev of host

Containers runtime may not pass /dev from host into the
container. If the /dev/ of the host is not accessible in the PMEM-CSI
container, access will fail to the newly created block device /dev/pmemX.Y
which will not be visible inside the container. The driver does not detect
the root cause of that problem during start-up, but only when a volume
creation has failed. This problem can be avoided by specifying an
explicit mount of /dev in the PMEM-CSI manifest.

## Repository elements that are generated or created separately

Here are creation and update notes for the elements in the repository
that are not hand-edited.

### Top-level README diagrams describing LVM and Direct device modes

Two diagrams are created with
[_dia_ drawing program](https://en.wikipedia.org/wiki/Dia_(software)).
The [single source file](/docs/diagrams/pmem-csi.dia) has
layers: {common, lvm, direct} so that two diagram variants can be produced
from a single source. Image files are produced by saving in PNG format with
correct set of layers visible.
The PNG files are committed as repository elements in
docs/images/devicemodes/.

### Top-level README diagram describing communication channels

This diagram was created with the
[_dia_ drawing program](https://en.wikipedia.org/wiki/Dia_(software)) using
a [source file](/docs/diagrams/pmem-csi-communication-diagram.dia).

An image file is produced by saving in PNG format.
The PNG file is committed as a
[repository element](/docs/images/communication/pmem-csi-communication-diagram.png).

### Diagrams describing provisioning sequence

Two diagrams are generated using the
[plantuml program](http://plantuml.com/).
Source files:
- [source file for regular sequence](/docs/diagrams/sequence.wsd)

The PNG files are committed as repository elements in docs/images/sequence/.

### Table of Contents in README and DEVELOPMENT

A Table of Contents (TOC) can be generated using multiple methods.

- One method  is to use [pandoc](https://pandoc.org/)

    ``` console
    $ pandoc -s -t markdown_github --toc README.md -o /tmp/temp.md
    ```

    Then check and hand-pick generated TOC part(s) from /tmp/temp.md and insert
    them in the desired location.
    Note that pandoc is known to produce incorrect TOC entries if headers
    contain special characters. This means that TOC generation will be more
    reliable if we avoid non-letter-or-number characters in the headers.

- Another method is to use the emacs command markdown-toc-generate-toc and
  manually check and edit the generated part: we do not show generated
  third-level headings in README.md.

## Build, edit, and deploy the Read the Docs site

The PMEM-CSI documentation is available as in-repo READMEs and as a GitHub\*
hosted [website](https://intel.github.io/pmem-csi). The website is created
using the [Sphinx](https://www.sphinx-doc.org/) documentation generator and
the well-known [Read the Docs](https://sphinx-rtd-theme.readthedocs.io/)
theme.

### Build

Building the documentation requires Python\* 3.x and venv.

``` console
$ make vhtml
```

### Edit

Sphinx uses [reStructuredText](https://docutils.sourceforge.io/docs/ref/rst/restructuredtext.html) (reST) as the primary document source type but can be
extended to use Markdown by adding the ``recommonmark`` and
``sphinx_markdown_tables`` extensions (see [conf.json](/conf.json)).

Change the navigation tree or add documents by updating the ``toctree``. The
main ``toctree`` is in ``index.rst``:

``` rst
.. toctree::
   :maxdepth: 2

   README.md
   docs/design.md
   docs/install.md
   docs/DEVELOPMENT.md
   docs/autotest.md
   examples/readme.rst
   Project GitHub repository <https://github.com/intel/pmem-csi>
```

reST files, Markdown files, and URLs can be added to a ``toctree``. The
``:maxdepth:`` argument dictates the number of header levels to be
displayed on that page. This website replaces the ``index.html`` output of
this project with a redirect to ``README.html`` (the conversion of the top
level README) to more closely match the in-repo documentation.

Any reST or Markdown file not referenced by a ``toctree`` will generate a
warning in the build. This document has a ``toctree`` in:

1. ``index.rst``
2.  ``examples/readme.rst``

Files or directories that are intentionally not referenced can be excluded
in [`conf.json`](/conf.json).

NOTE: Though GitHub can parse reST files, the ``toctree`` directive is Sphinx
specific, so it is not understood by GitHub. ``examples/readme.rst`` is a
good example. Adding the ``:hidden:`` argument to the ``toctree`` directive
means that the ``toctree`` is not displayed in the Sphinx built version of
the page.

### Custom link handling

This project has some custom capabilities added to the [conf.py](/conf.py) to
fix or improve how Sphinx generates the HTML site.

1. Markdown files: Converts references to Markdown files that include
anchors.
   ``` md
   [configuration options](autotest.md#configuration-options)
   ```
2. reST files: Fixes explicit links to Markdown files.
   ``` rst
   `Google Cloud Engine <gce.md>`__
   ```
3. Markdown files: Fixes references to reST files.
   ``` md
   [Application examples](examples/readme.rst)
   ```
4. Markdown files: Fixes links to files and directories within the GitHub
   repo.
   ``` md
   [Makefile](/Makefile)
   [deploy/kustomize](/deploy/kustomize)
   ```
   Links to files can be fixed in one of two ways, which can be set in the
   [conf.py](/conf.py). 

   ``` python
   baseBranch = "devel"
   useGitHubURL = True
   commitSHA = getenv('GITHUB_SHA')
   githubBaseURL = "https://github.com/intelkevinputnam/pmem-csi/"
   ```

   If ``useGitHubURL`` is set to True, it will try to create links based on
   your ``githubBaseURL`` and the SHA for the commit to the GitHub repo
   determined by the GitHub workflow on merge. If there is no SHA available,
   it will use the value of ``baseBranch``.

   If ``useGitHubURL`` is set to False, it will copy the files to the HTML
   output directory and provide links to that location.

   NOTE: Links to files and directories should use absolute paths relative
   to the repo (see Makefile and deploy/kustomize above). This will work
   both for the Sphinx build and when viewing in the GitHub repo.

   Links to directories are always converted to links to the GitHub
   repository.

### Deploying with GitHub actions

The publish
[workflow](https://github.com/intel/pmem-csi/blob/devel/.github/workflows/publish.yml)
is run each time a commit is made to the branches references by that file and pushes
the rendered HTML to the gh-pages branch. Other rules can be created
for other branches.

NOTE: Create a secret called ``ACCESS_TOKEN`` in repo>settings>secrets with
a [token](https://help.github.com/en/articles/creating-a-personal-access-token-for-the-command-line) generated by a user
with write privileges to enable the automated push to the gh-pages branch.
