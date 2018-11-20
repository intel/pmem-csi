# Downloads and unpacks the latest Clear Linux KVM image.
# This intentionally uses a different directory, otherwise
# we would end up sending the KVM images to the Docker
# daemon when building new Docker images as part of the
# build context.
#
# Sets the image up so that "ssh" works as root with a random
# password (stored in _work/passwd) and with _work/id as
# new private ssh key.
#
# Using chat for this didn't work because chat connected to
# qemu via pipes complained about unsupported ioctls. Expect
# would have been another alternative, but wasn't tried.
#
# Using plain bash might be a bit more brittle and harder to read, but
# at least it avoids extra dependencies.  Inspired by
# http://wiki.bash-hackers.org/syntax/keywords/coproc
#
# A registry on the build host (i.e. localhost:5000) is marked
# as insecure in Clear Linux under the hostname of the build host.
# Otherwise pulling images fails.
#
# The latest upstream Kubernetes binaries are used because that way
# the resulting installation is always up-to-date. Some workarounds
# in systemd units are necessary to get that up and running.
#
# The resulting cluster has:
# - a single node with the master taint removed
# - networking managed by kubelet itself
#
# Kubernetes does not get started by default because it might
# not always be needed in the image, depending on the test.
# _work/kube-clear-kvm can be used to start it.

# Sanitize proxy settings (accept upper and lower case, set and export upper
# case) and add local machine to no_proxy because some tests may use a
# local Docker registry. Also exclude 0.0.0.0 because otherwise Go
# tests using that address try to go through the proxy.
HTTP_PROXY=$(shell echo "$${HTTP_PROXY:-$${http_proxy}}")
HTTPS_PROXY=$(shell echo "$${HTTPS_PROXY:-$${https_proxy}}")
NO_PROXY=$(shell echo "$${NO_PROXY:-$${no_proxy}},$$(ip addr | grep inet6 | grep /64 | sed -e 's;.*inet6 \(.*\)/64 .*;\1;' | tr '\n' ','; ip addr | grep -w inet | grep /24 | sed -e 's;.*inet \(.*\)/24 .*;\1;' | tr '\n' ',')",0.0.0.0,10.0.2.15)
export HTTP_PROXY HTTPS_PROXY NO_PROXY
PROXY_ENV=env 'HTTP_PROXY=$(HTTP_PROXY)' 'HTTPS_PROXY=$(HTTPS_PROXY)' 'NO_PROXY=$(NO_PROXY)'

_work/clear-kvm-original.img:
	$(DOWNLOAD_CLEAR_IMG)

# This picks the latest available version. Can be overriden via make CLEAR_IMG_VERSION=
CLEAR_IMG_VERSION = $(shell curl https://download.clearlinux.org/latest)

DOWNLOAD_CLEAR_IMG = true
DOWNLOAD_CLEAR_IMG += && mkdir -p _work
DOWNLOAD_CLEAR_IMG += && cd _work
DOWNLOAD_CLEAR_IMG += && dd if=/dev/random bs=1 count=8 2>/dev/null | od -A n -t x8 | sed -e 's/ //g' >passwd
DOWNLOAD_CLEAR_IMG += && version=$(CLEAR_IMG_VERSION)
DOWNLOAD_CLEAR_IMG += && [ "$$version" ]
DOWNLOAD_CLEAR_IMG += && curl -O https://download.clearlinux.org/releases/$$version/clear/clear-$$version-kvm.img.xz
DOWNLOAD_CLEAR_IMG += && curl -O https://download.clearlinux.org/releases/$$version/clear/clear-$$version-kvm.img.xz-SHA512SUMS
DOWNLOAD_CLEAR_IMG += && curl -O https://download.clearlinux.org/releases/$$version/clear/clear-$$version-kvm.img.xz-SHA512SUMS.sig
# skipping image verification, does not work at the moment (https://github.com/clearlinux/distribution/issues/85)
# DOWNLOAD_CLEAR_IMG += && openssl smime -verify -in clear-$$version-kvm.img.xz-SHA512SUMS.sig -inform der -content clear-$$version-kvm.img.xz-SHA512SUMS -CAfile ../test/ClearLinuxRoot.pem -out /dev/null
DOWNLOAD_CLEAR_IMG += && sed -e 's;/.*/;;' clear-$$version-kvm.img.xz-SHA512SUMS | sha512sum -c
DOWNLOAD_CLEAR_IMG += && unxz -c <clear-$$version-kvm.img.xz >clear-kvm-original.img

# Number of nodes to be created in the virtual cluster, including master node.
NUM_NODES = 4

# Multiple different images can be created, starting with clear-kvm.0.img
# and ending with clear-kvm.<NUM_NODES - 1>.img.
#
# They have fixed IP addresses starting with 192.168.7.2 and host names
# kubernetes-0/1/2/.... The first image is for the Kubernetes master node,
# but configured so that also normal apps can run on it, i.e. no additional
# worker nodes are needed.
_work/clear-kvm.img: test/setup-clear-kvm.sh _work/clear-kvm-original.img _work/kube-clear-kvm _work/OVMF.fd _work/start-clear-kvm _work/id
	$(PROXY_ENV) test/setup-clear-kvm.sh $(NUM_NODES)
	ln -sf clear-kvm.0.img $@

_work/start-clear-kvm: test/start_qemu.sh
	mkdir -p _work
	cp $< $@
	sed -i -e "s;\(OVMF.fd\);$$(pwd)/_work/\1;g" $@
	chmod a+x $@

_work/kube-clear-kvm: test/start_kubernetes.sh
	mkdir -p _work
	cp $< $@
	sed -i -e "s;SSH;$$(pwd)/_work/ssh-clear-kvm;g" $@
	chmod u+x $@

_work/OVMF.fd:
	mkdir -p _work
	curl -o $@ https://download.clearlinux.org/image/OVMF.fd

_work/id:
	mkdir -p _work
	ssh-keygen -N '' -f $@
