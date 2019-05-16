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
CMDS=pmem-csi-driver pmem-vgm pmem-ns-init
TEST_CMDS=$(addsuffix -test,$(CMDS))
SHELL=bash
OUTPUT_DIR=_output
.DELETE_ON_ERROR:

ifeq ($(VERSION), )
VERSION=$(shell git describe --long --dirty --tags --match='v*')
endif

REGISTRY_NAME=localhost:5000
IMAGE_VERSION=canary
IMAGE_TAG=$(REGISTRY_NAME)/pmem-csi-driver$*:$(IMAGE_VERSION)
IMAGE_BUILD_ARGS=--build-arg NDCTL_VERSION=64.1 --build-arg NDCTL_CONFIGFLAGS='--libdir=/usr/lib --disable-docs --without-systemd --without-bash' \
--build-arg NDCTL_BUILD_DEPS='os-core-dev devpkg-util-linux devpkg-kmod devpkg-json-c file'
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

# An alias for "make build" and the default target.
all: build

# Build all binaries, including tests.
# Must use the workaround from https://github.com/golang/go/issues/15513
build: $(CMDS) $(TEST_CMDS)
	go test -run none ./pkg/... ./test/e2e

# "make test" runs a variety of fast tests, including building all source code.
# More tests are added elsewhere in this Makefile and test/test.make.
test: build

# Build production binaries.
$(CMDS):
	GOOS=linux go build -ldflags '-X main.version=${VERSION}' -a -o ${OUTPUT_DIR}/$@ ./cmd/$@

# Build a test binary that can be used instead of the normal one with
# additional "-run" parameters. In contrast to the normal it then also
# supports -test.coverprofile.
$(TEST_CMDS): %-test:
	GOOS=linux go test --cover -covermode=atomic -c -coverpkg=./pkg/... -ldflags '-X github.com/intel/pmem-csi/pkg/$*.version=${VERSION}' -o ${OUTPUT_DIR}/$@ ./cmd/$*

# The default is to refresh the base image once a day when building repeatedly.
# This is achieved by passing a fake variable that changes its value once per day.
# A CI system that produces production images should instead use
# `make  BUILD_IMAGE_ID=<some unique number>`.
#
# At the moment this build ID is not recorded in the resulting images.
# The VERSION variable should be used for that, if desired.
BUILD_IMAGE_ID=$(shell date +%Y-%m-%d)

# Build and publish images for production or testing (i.e. with test binaries).
build-images: build-image build-test-image
push-images: push-image push-test-image
build-image build-test-image: build%-image:
	docker build --pull --build-arg CACHEBUST=$(BUILD_IMAGE_ID) --build-arg BIN_SUFFIX=$(findstring -test,$*) $(BUILD_ARGS) $(IMAGE_BUILD_ARGS) -t $(IMAGE_TAG) -f ./Dockerfile . --label revision=$(VERSION)
push-image push-test-image: push%-image: build%-image
	docker push $(IMAGE_TAG)

clean:
	go clean -r -x
	-rm -rf $(OUTPUT_DIR)

.PHONY: all build test clean $(CMDS) $(TEST_CMDS)

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

# Build kustomize at a certain revision. Depends on go >= 1.11
# because we use module support.
KUSTOMIZE_VERSION=e42933ec54ce9a65f65e125a1ccf482927f0e515
_work/kustomize-$(KUSTOMIZE_VERSION):
	tmpdir=`mktemp -d` && \
	trap 'rm -r $$tmpdir' EXIT && \
	cd $$tmpdir && \
	echo "module foo" >go.mod && \
	go get sigs.k8s.io/kustomize@$(KUSTOMIZE_VERSION) && \
	go build -o $(abspath $@) sigs.k8s.io/kustomize
	ln -sf $(@F) _work/kustomize

# We generate deployment files with kustomize and include the output
# in the git repo because not all users will have kustomize or it
# might be an unsuitable version. When any file changes, update the
# output.
KUSTOMIZE_INPUT := $(shell find deploy/kustomize -type f)

# Output files and their corresponding kustomize target.
# The "testing" flavor of the generated files contains both
# the loglevel changes and enables coverage data collection.
KUSTOMIZE_OUTPUT :=
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.13/pmem-csi-direct.yaml
KUSTOMIZATION_deploy/kubernetes-1.13/pmem-csi-direct.yaml = deploy/kustomize/kubernetes-1.13-direct
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.13/pmem-csi-lvm.yaml
KUSTOMIZATION_deploy/kubernetes-1.13/pmem-csi-lvm.yaml = deploy/kustomize/kubernetes-1.13-lvm
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.13/pmem-csi-direct-testing.yaml
KUSTOMIZATION_deploy/kubernetes-1.13/pmem-csi-direct-testing.yaml = deploy/kustomize/kubernetes-1.13-direct-coverage
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.13/pmem-csi-lvm-testing.yaml
KUSTOMIZATION_deploy/kubernetes-1.13/pmem-csi-lvm-testing.yaml = deploy/kustomize/kubernetes-1.13-lvm-coverage
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.14/pmem-csi-direct.yaml
KUSTOMIZATION_deploy/kubernetes-1.14/pmem-csi-direct.yaml = deploy/kustomize/kubernetes-1.14-direct
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.14/pmem-csi-lvm.yaml
KUSTOMIZATION_deploy/kubernetes-1.14/pmem-csi-lvm.yaml = deploy/kustomize/kubernetes-1.14-lvm
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.14/pmem-csi-direct-testing.yaml
KUSTOMIZATION_deploy/kubernetes-1.14/pmem-csi-direct-testing.yaml = deploy/kustomize/kubernetes-1.14-direct-coverage
KUSTOMIZE_OUTPUT += deploy/kubernetes-1.14/pmem-csi-lvm-testing.yaml
KUSTOMIZATION_deploy/kubernetes-1.14/pmem-csi-lvm-testing.yaml = deploy/kustomize/kubernetes-1.14-lvm-coverage
KUSTOMIZE_OUTPUT += deploy/common/pmem-storageclass-ext4.yaml
KUSTOMIZATION_deploy/common/pmem-storageclass-ext4.yaml = deploy/kustomize/storageclass-ext4
KUSTOMIZE_OUTPUT += deploy/common/pmem-storageclass-xfs.yaml
KUSTOMIZATION_deploy/common/pmem-storageclass-xfs.yaml = deploy/kustomize/storageclass-xfs
KUSTOMIZE_OUTPUT += deploy/common/pmem-storageclass-cache.yaml
KUSTOMIZATION_deploy/common/pmem-storageclass-cache.yaml = deploy/kustomize/storageclass-cache
kustomize: $(KUSTOMIZE_OUTPUT)
$(KUSTOMIZE_OUTPUT): _work/kustomize-$(KUSTOMIZE_VERSION) $(KUSTOMIZE_INPUT)
	$< build --load_restrictor none $(KUSTOMIZATION_$@) >$@

# Always re-generate the output files because "git rebase" might have
# left us with an inconsistent state.
.PHONY: kustomize $(KUSTOMIZE_OUTPUT)

.PHONY: clean-kustomize
clean: clean-kustomize
clean-kustomize:
	rm -f _work/kustomize-*
	rm -f _work/kustomize

.PHONY: test-kustomize $(addprefix test-kustomize-,$(KUSTOMIZE_OUTPUT))
test: test-kustomize
test-kustomize: $(addprefix test-kustomize-,$(KUSTOMIZE_OUTPUT))
$(addprefix test-kustomize-,$(KUSTOMIZE_OUTPUT)): test-kustomize-%: _work/kustomize
	@ if ! diff <($< build --load_restrictor none $(KUSTOMIZATION_$*)) $*; then echo "$* was modified manually" && false; fi
