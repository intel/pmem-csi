# Copyright 2017 The Kubernetes Authors.
# Copyright 2018 Intel Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

IMPORT_PATH=github.com/intel/pmem-csi
SHELL=bash
.DELETE_ON_ERROR:

ifeq ($(VERSION), )
VERSION=$(shell git describe --long --dirty --tags --match='v*')
endif

REGISTRY_NAME=localhost:5000
IMAGE_VERSION_pmem-csi-driver=canary
IMAGE_VERSION_pmem-ns-init=canary
IMAGE_VERSION_pmem-vgm=canary
IMAGE_TAG=$(REGISTRY_NAME)/$*:$(IMAGE_VERSION_$*)
IMAGE_BUILD_ARGS=--build-arg NDCTL_VERSION=64.1 --build-arg NDCTL_CONFIGFLAGS='--libdir=/usr/lib --disable-docs --without-systemd' --build-arg NDCTL_BUILD_DEPS='build-base autoconf automake bash-completion libtool libuuid json-c kmod asciidoc xmlto kmod-dev eudev-dev util-linux-dev json-c-dev linux-headers wget tar file keyutils-dev'
# Pass proxy config via --build-arg only if these are set,
# enabling proxy config other way, like ~/.docker/config.json
BUILD_ARGS=
ifneq ($(http_proxy),)
	BUILD_ARGS:=${BUILD_ARGS} --build-arg http_proxy=${http_proxy}
endif
ifneq ($(https_proxy),)
	BUILD_ARGS:=${BUILD_ARGS} --build-arg https_proxy=${https_proxy}
endif
ifneq ($(no_proxy),)
	BUILD_ARGS:=${BUILD_ARGS} --build-arg no_proxy=${no_proxy}
endif

BUILD_ARGS:=${BUILD_ARGS} --build-arg VERSION=${VERSION}

# Build main set of components.
all: pmem-csi-driver pmem-ns-init pmem-vgm

# Build all binaries, including tests.
# Must use the workaround from https://github.com/golang/go/issues/15513
build: all
	go test -run none ./pkg/... ./test/e2e

build-images: build-pmem-csi-driver-image build-pmem-ns-init-image build-pmem-vgm-image

push-images: push-pmem-csi-driver-image push-pmem-ns-init-image push-pmem-vgm-image


pmem-csi-driver pmem-vgm pmem-ns-init:
	GOOS=linux go build -ldflags '-X main.version=${VERSION}' -a -o _output/$@ ./cmd/$@

build-%-image:
	docker build ${BUILD_ARGS} ${IMAGE_BUILD_ARGS} -t $(IMAGE_TAG) -f ./cmd/$*/Dockerfile . --label revision=${VERSION}

push-%-image: build-%-image
	docker push $(IMAGE_TAG)

clean:
	go clean -r -x
	-rm -rf _output

.PHONY: all test clean pmem-csi-driver pmem-ns-init pmem-vgm

# Add support for creating and booting a cluster under QEMU.
# All of the commands operate on a cluster stored in _work/$(CLUSTER),
# which defaults to _work/clear-kvm. This can be changed with
# make variables, for example:
#   make CLUSTER=clear-kvm-28070 CLEAR_IMG_VERSION=28070 start
#
# All clusters called "clear-kvm[-something]" are created with
# test/clear-kvm.make. They run inside QEMU and share the
# same IP addresses, and thus cannot run in parallel.
CLUSTER := clear-kvm
include test/clear-kvm.make
include test/start-stop.make
include test/test.make



# We generate deployment files with kustomize and include the output
# in the git repo because not all users will have kustomize or it
# might be an unsuitable version. When any file changes, update the
# output.
KUSTOMIZE_INPUT := $(shell find deploy/kustomize -type f)

# Workaround for https://github.com/kubernetes-sigs/kustomize/issues/937:
# we have to copy patch files into targets instead of referencing
# with a relative path to the parent directory.
KUSTOMIZE_INPUT += deploy/kustomize/kubernetes-1.13-lvm-testing/controller-socat-patch.yaml
KUSTOMIZE_INPUT += deploy/kustomize/kubernetes-1.13-direct-testing/controller-socat-patch.yaml
deploy/kustomize/kubernetes-%/controller-socat-patch.yaml: deploy/kustomize/testing/controller-socat-patch.yaml
	cp $< $@

# Output files and their corresponding kustomize target.
KUSTOMIZE_OUTPUT :=
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.13/pmem-csi-direct.yaml
KUSTOMIZATION_deploy/kubernetes-1.13/pmem-csi-direct.yaml = deploy/kustomize/kubernetes-1.13-direct
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.13/pmem-csi-lvm.yaml
KUSTOMIZATION_deploy/kubernetes-1.13/pmem-csi-lvm.yaml = deploy/kustomize/kubernetes-1.13-lvm
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.13/pmem-csi-direct-testing.yaml
KUSTOMIZATION_deploy/kubernetes-1.13/pmem-csi-direct-testing.yaml = deploy/kustomize/kubernetes-1.13-direct-testing
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.13/pmem-csi-lvm-testing.yaml
KUSTOMIZATION_deploy/kubernetes-1.13/pmem-csi-lvm-testing.yaml = deploy/kustomize/kubernetes-1.13-lvm-testing
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.13/pmem-storageclass-ext4.yaml
KUSTOMIZATION_deploy/kubernetes-1.13/pmem-storageclass-ext4.yaml = deploy/kustomize/storageclass-ext4
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.13/pmem-storageclass-xfs.yaml
KUSTOMIZATION_deploy/kubernetes-1.13/pmem-storageclass-xfs.yaml = deploy/kustomize/storageclass-xfs
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.13/pmem-storageclass-cache.yaml
KUSTOMIZATION_deploy/kubernetes-1.13/pmem-storageclass-cache.yaml = deploy/kustomize/storageclass-cache
kustomize: $(KUSTOMIZE_OUTPUT)
$(KUSTOMIZE_OUTPUT): _work/kustomize $(KUSTOMIZE_INPUT)
	$< build $(KUSTOMIZATION_$@) >$@


# Build kustomize at a certain revision. Depends on go >= 1.12
# because we use module support.
KUSTOMIZE_VERSION=177297c0efb92ec2d7eea8f20c65c1d11c6ae7ef
_work/kustomize: _work/.kustomize-$(KUSTOMIZE_VERSION)-stamp
_work/.kustomize-$(KUSTOMIZE_VERSION)-stamp:
	tmpdir=`mktemp -d` && \
	trap 'rm -r $$tmpdir' EXIT && \
	cd $$tmpdir && \
	echo "module foo" >go.mod && \
	go get sigs.k8s.io/kustomize@$(KUSTOMIZE_VERSION) && \
	go build -o $(abspath _work/kustomize) sigs.k8s.io/kustomize && \
	touch --reference=$(abspath _work/kustomize) $(abspath $@)

PHONY: clean-kustomize
clean: clean-kustomize
clean-kustomize:
	rm -f _work/kustomize
	rm -f _work/.kustomize-$(KUSTOMIZE_VERSION)-stamp
	rm -f deploy/kustomize/kubernetes-*/controller-socat-patch.yaml

PHONY: test-kustomize $(addprefix test-kustomize-,$(KUSTOMIZE_OUTPUT))
test: test-kustomize
test-kustomize: $(addprefix test-kustomize-,$(KUSTOMIZE_OUTPUT))
$(addprefix test-kustomize-,$(KUSTOMIZE_OUTPUT)): test-kustomize-%: _work/kustomize
	@ if ! diff <($< build $(KUSTOMIZATION_$*)) $*; then echo "$* was modified manually" && false; fi
