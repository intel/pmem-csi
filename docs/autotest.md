# Automated testing

- [Automated testing](#automated-testing)
    - [Unit testing and code quality](#unit-testing-and-code-quality)
    - [QEMU and Kubernetes](#qemu-and-kubernetes)
    - [Starting and stopping a test cluster](#starting-and-stopping-a-test-cluster)
    - [Running commands on test cluster nodes over ssh](#running-commands-on-test-cluster-nodes-over-ssh)
    - [Configuration options](#configuration-options)
    - [Running E2E tests](#running-e2e-tests)

## Unit testing and code quality

Use the `make test` command.

## QEMU and Kubernetes

E2E testing relies on a cluster running inside multiple QEMU virtual
machines deployed by [GoVM](https://github.com/govm-project/govm). The
same cluster can also be used interactively when real hardware is not
available.

E2E testing is known to work on a Linux development host system. The user
must be allowed to use Docker.

KVM must be enabled. Usually this is the case when `/dev/kvm` exists.
The current user does not need the privileges to use KVM and QEMU
doesn't have to be installed because GoVM will run QEMU inside a
container with root privileges.

Note that cloud providers often don't offer KVM support on their
regular machines. Search for "nested virtualization" for your provider
to determine whether and how it supports KVM.

Nested virtualization is also needed when using Kata Containers inside
the cluster. On Intel-based machines it can be enabled by loading the
`kvm_intel` module with `nested=1` (see
https://wiki.archlinux.org/index.php/KVM#Nested_virtualization). At
this time, Kata Containers up to and including 1.9.1 is [not
compatible with
PMEM-CSI](https://github.com/intel/pmem-csi/issues/303) because
volumes are not passed in as PMEM, but Kata Containers [can be
installed](https://github.com/kata-containers/packaging/tree/master/kata-deploy#kubernetes-quick-start)
and used for applications that are not using PMEM.

The `clear-cloud` image is downloaded automatically. By default,
four different virtual machines are prepared. Each image is pre-configured
with its own hostname and with network.

The images will contain the latest
[Clear Linux OS](https://clearlinux.org/) and have the Kubernetes
version supported by Clear Linux installed.

PMEM-CSI images must have been created and published in some Docker
registry, as described earlier in [build PMEM-CSI](DEVELOPMENT.md#build-pmem-csi).
In addition, that registry must be accessible from inside the
cluster. That works for the default (a local registry in the build
host) but may require setting additional [configuration
options](#configuration-options) for other scenarios.

## Starting and stopping a test cluster

`make start` will bring up a Kubernetes test cluster inside four QEMU
virtual machines.
The first node is the Kubernetes master without
persistent memory.
The other three nodes are worker nodes with one emulated 32GB NVDIMM each.
After the cluster has been formed, `make start` adds `storage=pmem` label
to the worker nodes and deploys the PMEM-CSI driver.
Once `make start` completes, the cluster is ready for interactive use via
`kubectl` inside the virtual machine. Alternatively, you can also
set `KUBECONFIG` as shown at the end of the `make start` output
and use `kubectl` binary on the host running VMs.

Use `make stop` to stop and remove the virtual machines.

`make restart` can be used to cleanly reboot all virtual
machines. This is useful during development after a `make push-images`
to ensure that the cluster runs those rebuilt images.

## Running commands on test cluster nodes over ssh

`make start` generates ssh wrapper scripts `_work/pmem-govm/ssh.N` for each
test cluster node which are handy for running a single command or to
start an interactive shell. Examples:

`_work/pmem-govm/ssh.0 kubectl get pods` runs a kubectl command on
the master node.

`_work/pmem-govm/ssh.1` starts a shell on the first worker node.

## Deploying PMEM-CSI on a test cluster

After `make start`, PMEM-CSI is *not* installed yet. Either install
manually as [described for a normal
cluster](#run-pmem-csi-on-kubernetes) or use the
[setup-deployment.sh](/test/setup-deployment.sh) script.

## Configuration options

Several aspects of the cluster and build setup can be configured by overriding
the settings in the [test-config.sh](/test/test-config.sh) file. See
that file for a description of all options. Options can be set as
environment variables of `make start` on a case-by-case basis or
permanently by creating a file like `test/test-config.d/my-config.sh`.

Multiple different clusters can be brought up in parallel by changing
the default `pmem-govm` cluster name via the `CLUSTER` env variable.

For example, this invocation sets up a cluster using the non-default
Fedora distro:

``` 
TEST_DISTRO=fedora CLUSTER=fedora-govm make start
```

See additional details in [test/test-config.d](/test/test-config.d).

## Running E2E tests

`make test_e2e` will run [csi-test
sanity](https://github.com/kubernetes-csi/csi-test/tree/master/pkg/sanity)
tests and some [Kubernetes storage
tests](https://github.com/kubernetes/kubernetes/tree/master/test/e2e/storage/testsuites)
against the PMEM-CSI driver.

When [ginkgo](https://onsi.github.io/ginkgo/) is installed, then it
can be used to run individual tests and to control additional aspects
of the test run. For example, to run just the E2E provisioning test
(create PVC, write data in one pod, read it in another) in verbose mode:

``` console
$ KUBECONFIG=$(pwd)/_work/pmem-govm/kube.config REPO_ROOT=$(pwd) ginkgo -v -focus=pmem-csi.*should.provision.storage.with.defaults ./test/e2e/
Nov 26 11:21:28.805: INFO: The --provider flag is not set.  Treating as a conformance test.  Some tests may not be run.
Running Suite: PMEM E2E suite
=============================
Random Seed: 1543227683 - Will randomize all specs
Will run 1 of 61 specs

Nov 26 11:21:28.812: INFO: checking config
Nov 26 11:21:28.812: INFO: >>> kubeConfig: /nvme/gopath/src/github.com/intel/pmem-csi/_work/pmem-govm/kube.config
Nov 26 11:21:28.817: INFO: Waiting up to 30m0s for all (but 0) nodes to be schedulable
...
Ran 1 of 61 Specs in 58.465 seconds
SUCCESS! -- 1 Passed | 0 Failed | 0 Pending | 60 Skipped
PASS

Ginkgo ran 1 suite in 1m3.850672246s
Test Suite Passed
```

It is also possible to run just the sanity tests until one of them fails:

``` console
$ REPO_ROOT=`pwd` ginkgo '-focus=sanity' -failFast ./test/e2e/
...
```