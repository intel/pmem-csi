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
export PWD=$(shell pwd)
OUTPUT_DIR=_output
.DELETE_ON_ERROR:

ifeq ($(VERSION), )
VERSION=$(shell git describe --long --dirty --tags --match='v*')
endif

# Sanitize proxy settings (accept upper and lower case, set and export upper
# case) and add local machine to no_proxy because some tests may use a
# local Docker registry. Also exclude 0.0.0.0 because otherwise Go
# tests using that address try to go through the proxy.
HTTP_PROXY=$(shell echo "$${HTTP_PROXY:-$${http_proxy}}")
HTTPS_PROXY=$(shell echo "$${HTTPS_PROXY:-$${https_proxy}}")
NO_PROXY=$(shell echo "$${NO_PROXY:-$${no_proxy}},$$(ip addr | grep inet6 | grep /64 | sed -e 's;.*inet6 \(.*\)/64 .*;\1;' | tr '\n' ','; ip addr | grep -w inet | grep /24 | sed -e 's;.*inet \(.*\)/24 .*;\1;' | tr '\n' ',')",0.0.0.0,10.0.2.15)
export HTTP_PROXY HTTPS_PROXY NO_PROXY

REGISTRY_NAME?=$(shell . test/test-config.sh && echo $${TEST_BUILD_PMEM_REGISTRY})
IMAGE_VERSION?=v0.5.25
IMAGE_TAG=$(REGISTRY_NAME)/pmem-csi-driver$*:$(IMAGE_VERSION)
# Pass proxy config via --build-arg only if these are set,
# enabling proxy config other way, like ~/.docker/config.json
BUILD_ARGS?=
ifneq ($(HTTP_PROXY),)
	BUILD_ARGS:=${BUILD_ARGS} --build-arg http_proxy=${HTTP_PROXY}
endif
ifneq ($(HTTPS_PROXY),)
	BUILD_ARGS:=${BUILD_ARGS} --build-arg https_proxy=${HTTPS_PROXY}
endif
ifneq ($(NO_PROXY),)
	BUILD_ARGS:=${BUILD_ARGS} --build-arg no_proxy=${NO_PROXY}
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
	GOOS=linux go build -ldflags '-X github.com/intel/pmem-csi/pkg/$@.version=${VERSION}' -a -o ${OUTPUT_DIR}/$@ ./cmd/$@

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
BUILD_IMAGE_ID?=$(shell date +%Y-%m-%d)

# Build and publish images for production or testing (i.e. with test binaries).
# Pushing images also automatically rebuilds the image first. This can be disabled
# with `make push-images PUSH_IMAGE_DEP=`.
build-images: build-image build-test-image
push-images: push-image push-test-image
build-image build-test-image: build%-image:
	docker build --pull --build-arg CACHEBUST=$(BUILD_IMAGE_ID) --build-arg BIN_SUFFIX=$(findstring -test,$*) $(BUILD_ARGS) -t $(IMAGE_TAG) -f ./Dockerfile . --label revision=$(VERSION)
PUSH_IMAGE_DEP = build%-image
push-image push-test-image: push%-image: $(PUSH_IMAGE_DEP)
	docker push $(IMAGE_TAG)

.PHONY: print-image-version
print-image-version:
	@ echo "$(IMAGE_VERSION)"

clean:
	go clean -r -x
	-rm -rf $(OUTPUT_DIR)

.PHONY: all build test clean $(CMDS) $(TEST_CMDS)

# Add support for creating and booting a cluster under QEMU.
# All of the commands operate on a cluster stored in _work/$(CLUSTER),
# which defaults to _work/clear-govm. This can be changed with
# make variables, for example:
#   CLUSTER=clear-govm-crio TEST_CRI=crio make start
export CLUSTER ?= clear-govm
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
KUSTOMIZE_INPUT := $(shell [ ! -d deploy/kustomize ] || find deploy/kustomize -type f)

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
KUSTOMIZE_OUTPUT += deploy/common/pmem-storageclass-late-binding.yaml
KUSTOMIZATION_deploy/common/pmem-storageclass-late-binding.yaml = deploy/kustomize/storageclass-late-binding
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
$(addprefix test-kustomize-,$(KUSTOMIZE_OUTPUT)): test-kustomize-%: _work/kustomize-$(KUSTOMIZE_VERSION)
	@ if ! diff <($< build --load_restrictor none $(KUSTOMIZATION_$*)) $*; then echo "$* was modified manually" && false; fi
