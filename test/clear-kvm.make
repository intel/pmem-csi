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
# _work/clear-kvm/start-kubernetes can be used to start it.

# Sanitize proxy settings (accept upper and lower case, set and export upper
# case) and add local machine to no_proxy because some tests may use a
# local Docker registry. Also exclude 0.0.0.0 because otherwise Go
# tests using that address try to go through the proxy.
HTTP_PROXY=$(shell echo "$${HTTP_PROXY:-$${http_proxy}}")
HTTPS_PROXY=$(shell echo "$${HTTPS_PROXY:-$${https_proxy}}")
NO_PROXY=$(shell echo "$${NO_PROXY:-$${no_proxy}},$$(ip addr | grep inet6 | grep /64 | sed -e 's;.*inet6 \(.*\)/64 .*;\1;' | tr '\n' ','; ip addr | grep -w inet | grep /24 | sed -e 's;.*inet \(.*\)/24 .*;\1;' | tr '\n' ',')",0.0.0.0,10.0.2.15)
export HTTP_PROXY HTTPS_PROXY NO_PROXY
PROXY_ENV=env 'HTTP_PROXY=$(HTTP_PROXY)' 'HTTPS_PROXY=$(HTTPS_PROXY)' 'NO_PROXY=$(NO_PROXY)'

# This picks the latest available version. Can be overriden via make CLEAR_IMG_VERSION=
CLEAR_IMG_VERSION = $(shell curl --silent https://download.clearlinux.org/latest)

DOWNLOAD_CLEAR_IMG = true
DOWNLOAD_CLEAR_IMG += && mkdir -p _work
DOWNLOAD_CLEAR_IMG += && cd _work
DOWNLOAD_CLEAR_IMG += && version=$(CLEAR_IMG_VERSION)
DOWNLOAD_CLEAR_IMG += && [ "$$version" ]
DOWNLOAD_CLEAR_IMG += && curl -O https://download.clearlinux.org/releases/$$version/clear/clear-$$version-kvm.img.xz
DOWNLOAD_CLEAR_IMG += && curl -O https://download.clearlinux.org/releases/$$version/clear/clear-$$version-kvm.img.xz-SHA512SUMS
DOWNLOAD_CLEAR_IMG += && curl -O https://download.clearlinux.org/releases/$$version/clear/clear-$$version-kvm.img.xz-SHA512SUMS.sig
# skipping image verification, does not work at the moment (https://github.com/clearlinux/distribution/issues/85)
# DOWNLOAD_CLEAR_IMG += && openssl smime -verify -in clear-$$version-kvm.img.xz-SHA512SUMS.sig -inform der -content clear-$$version-kvm.img.xz-SHA512SUMS -CAfile ../test/ClearLinuxRoot.pem -out /dev/null
DOWNLOAD_CLEAR_IMG += && sed -e 's;/.*/;;' clear-$$version-kvm.img.xz-SHA512SUMS | sha512sum -c
DOWNLOAD_CLEAR_IMG += && unxz -c <clear-$$version-kvm.img.xz >clear-kvm-$$version.img

# Number of nodes to be created in the virtual cluster, including master node.
NUM_NODES = 4

# The following rules only apply when CLUSTER starts with clear-kvm.
# This is necessary because a rule of this format is not applied
# for CLUSTER=clear-kvm:
# _work/clear-kvm%/start-kubernetes: test/start_kubernetes.sh
#
# Explicitly listing _work/$(CLUSTER)/start-kubernetes as
# target works around that, but then we must avoid defining
# that rule when the name is different.
ifneq (,$(filter clear-kvm%,$(CLUSTER)))

# Multiple different images can be created, starting with clear-kvm.0.img
# and ending with clear-kvm.<NUM_NODES - 1>.img.
#
# They have fixed IP addresses starting with 192.168.7.2 and host names
# kubernetes-0/1/2/.... The first image is for the Kubernetes master node,
# but configured so that also normal apps can run on it, i.e. no additional
# worker nodes are needed.
_work/$(CLUSTER)/created: _work/clear-kvm%/created: test/setup-clear-kvm.sh _work/clear-kvm%/start-kubernetes _work/clear-kvm%/run-qemu _work/id _work/passwd
	if ! [ -f _work/clear-kvm-$(CLEAR_IMG_VERSION).img ]; then ( $(DOWNLOAD_CLEAR_IMG) ); fi
	$(PROXY_ENV) test/setup-clear-kvm.sh _work/clear-kvm-$(CLEAR_IMG_VERSION).img $(@D) $(NUM_NODES)
	touch $@
.SECONDARY: _work/$(CLUSTER)/start-kubernetes _work/$(CLUSTER)/run-qemu

# Makes a copy of the OVMF.fd because it might be modified by the
# running virtual machine. Strictly speaking, we want one copy per
# virtual machine instance.
_work/$(CLUSTER)/run-qemu: _work/clear-kvm%/run-qemu: test/start_qemu.sh _work/OVMF.fd
	mkdir -p $(@D)
	cp _work/OVMF.fd $(@D)
	sed -e "s;\(OVMF.fd\);$$(pwd)/$(@D)/\1;g" $< >$@
	chmod a+x $@
_work/$(CLUSTER)/start-kubernetes: _work/clear-kvm%/start-kubernetes: test/start_kubernetes.sh
	mkdir -p $(@D)
	sed -e "s;SSH;$$(pwd)/$(@D)/ssh;g" $< >$@
	chmod u+x $@

endif

# Generate a random password. Converting a random binary to hex still
# had many repetitive or adjacent characters, which was flagged as
# insecure by the pwquality-tools password check in Clear Linux. Now
# we generate a wider range of printable characters (one at a time,
# to avoid depleting the entropy pool) and in addition apply the same
# quality check if pwscore is installed.
_work/passwd:
	while true; do \
		rm -f $@; \
		len=0; \
		while [ $$len -lt 32 ]; do \
			c=$$(dd if=/dev/urandom bs=1 count=1 2>/dev/null| tr -dc _A-Z-a-z-0-9); \
			if [ "$$c" ]; then \
				echo -n "$$c" >>$@; \
				len=$$(($$len + 1)); \
			fi; \
		done; \
		if command -v pwscore >/dev/null; then \
			if pwscore <$@ >/dev/null; then \
				break; \
			fi; \
		else \
			break; \
		fi; \
	done

_work/OVMF.fd:
	mkdir -p $(@D)
	curl -o $@ https://download.clearlinux.org/image/OVMF.fd

_work/id:
	mkdir -p $(@D)
	ssh-keygen -N '' -f $@
