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

GO_BINARY=go
GO=GOOS=linux GO111MODULE=on $(GO_BINARY)
IMPORT_PATH=github.com/intel/pmem-csi
CMDS=pmem-csi-driver pmem-csi-operator
TEST_CMDS=$(addsuffix -test,$(CMDS))
SHELL=bash
export PWD=$(shell pwd)
OUTPUT_DIR=_output
.DELETE_ON_ERROR:

ifeq ($(VERSION), )
VERSION:=$(shell git describe --long --dirty --tags --match='v*')
endif

# VERSION is of the format vX.Y.Z[-<number of commits>-<short hash>|<suffix>].
# For the SDK we need just X.Y.Z.
MAJOR_MINOR_PATCH_VERSION:=$(shell echo $(VERSION) | cut -f1 -d'-' | sed -e 's/^v//')

# Sometimes just X.Y is needed.
MAJOR_MINOR_VERSION:=$(shell echo $(MAJOR_MINOR_PATCH_VERSION) | sed -e 's/\([0-9]*\.[0-9]*\)\..*/\1/')

# Sanitize proxy settings (accept upper and lower case, set and export upper
# case) and add local machine to no_proxy because some tests may use a
# local Docker registry. Also exclude 0.0.0.0 because otherwise Go
# tests using that address try to go through the proxy.
HTTP_PROXY=$(shell echo "$${HTTP_PROXY:-$${http_proxy}}")
HTTPS_PROXY=$(shell echo "$${HTTPS_PROXY:-$${https_proxy}}")
NO_PROXY=$(shell echo "$${NO_PROXY:-$${no_proxy}},$$(ip addr | grep inet6 | grep /64 | sed -e 's;.*inet6 \(.*\)/64 .*;\1;' | tr '\n' ','; ip addr | grep -w inet | grep -e '/\(24\|16\|8\)' | sed -e 's;.*inet \(.*\)/\(24\|16\|8\) .*;\1;' | tr '\n' ',')",0.0.0.0)
export HTTP_PROXY HTTPS_PROXY NO_PROXY

REGISTRY_NAME?=$(shell . test/test-config.sh && echo $${TEST_BUILD_PMEM_REGISTRY})
IMAGE_VERSION?=canary
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
build: $(CMDS) $(TEST_CMDS) check-go-version-$(GO_BINARY)
	$(GO) test -run none ./pkg/... ./test/e2e

# "make test" runs a variety of fast tests, including building all source code.
# More tests are added elsewhere in this Makefile and test/test.make.
test: build

# "make generate" invokes code generators.
generate: operator-generate-k8s

# Build production binaries.
$(CMDS): check-go-version-$(GO_BINARY)
	$(GO) build -ldflags '-X github.com/intel/pmem-csi/pkg/$@.version=${VERSION} -s -w' -a -o ${OUTPUT_DIR}/$@ ./cmd/$@

# Build a test binary that can be used instead of the normal one with
# additional "-run" parameters. In contrast to the normal it then also
# supports -test.coverprofile.
$(TEST_CMDS): %-test: check-go-version-$(GO_BINARY)
	$(GO) test --cover -covermode=atomic -c -coverpkg=./pkg/... -ldflags '-X github.com/intel/pmem-csi/pkg/$*.version=${VERSION}' -o ${OUTPUT_DIR}/$@ ./cmd/$*

# Set by the CI to ensure that image building really pulls a new base.
CACHEBUST=

# Build and publish images for production or testing (i.e. with test binaries).
# Pushing images also automatically rebuilds the image first. This can be disabled
# with `make push-images PUSH_IMAGE_DEP=`.
build-images: build-image build-test-image
push-images: push-image push-test-image
build-image build-test-image: build%-image: populate-vendor-dir
	docker build --pull --build-arg CACHEBUST=$(CACHEBUST) --build-arg GOFLAGS=-mod=vendor --build-arg BIN_SUFFIX=$(findstring -test,$*) $(BUILD_ARGS) -t $(IMAGE_TAG) -f ./Dockerfile . --label revision=$(VERSION)
PUSH_IMAGE_DEP = build%-image
# "docker push" has been seen to fail temporarily with "error creating overlay mount to /var/lib/docker/overlay2/xxx/merged: device or resource busy".
# Here we simply try three times before giving up.
push-image push-test-image: push%-image: $(PUSH_IMAGE_DEP)
	@ i=0; while true; do \
		if (set -x; docker push $(IMAGE_TAG)); then \
			exit 0; \
		elif [ $$i -ge 2 ]; then \
			echo "'docker push' failed repeatedly, giving up"; \
			exit; \
		else \
			echo "attempt #$$i: 'docker push' failed, will try again"; \
			i=$$(($$i + 1)); \
		fi; \
	done

# This ensures that all sources are available in the "vendor" directory for use
# inside "docker build".
populate-vendor-dir:
	go mod tidy
	go mod vendor

.PHONY: print-image-version
print-image-version:
	@ echo "$(IMAGE_VERSION)"

clean:
	$(GO) clean -r -x ./cmd/...
	-rm -rf $(OUTPUT_DIR) vendor

.PHONY: all build test clean $(CMDS) $(TEST_CMDS)

# Add support for creating and booting a cluster under QEMU.
# All of the commands operate on a cluster stored in _work/$(CLUSTER),
# which defaults to _work/clear-govm. This can be changed with
# make variables, for example:
#   CLUSTER=pmem-govm-crio TEST_CRI=crio make start
export CLUSTER ?= pmem-govm
include test/start-stop.make
include test/test.make

#Kustomize latest release version
KUSTOMIZE_VERSION=v4.0.5
_work/kustomize_${KUSTOMIZE_VERSION}_linux_amd64.tar.gz:
	curl -L https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize/${KUSTOMIZE_VERSION}/kustomize_${KUSTOMIZE_VERSION}_linux_amd64.tar.gz -o $(abspath $@)

_work/kustomize: _work/kustomize_${KUSTOMIZE_VERSION}_linux_amd64.tar.gz
	tar xzf $< -C _work
	touch $@

include operator/operator.make

# We generate deployment files with kustomize and include the output
# in the git repo because not all users will have kustomize or it
# might be an unsuitable version. When any file changes, update the
# output.
KUSTOMIZE_INPUT := $(shell [ ! -d deploy/kustomize ] || find deploy/kustomize -type f)

# Output files and their corresponding kustomization, in the format <target .yaml>=<kustomization directory>
KUSTOMIZE :=

# For each supported Kubernetes version, we provide four different flavors.
# The "testing" flavor of the generated files contains both
# the loglevel changes and enables coverage data collection.
KUSTOMIZE_KUBERNETES_OUTPUT = \
    deploy/kubernetes-X.XX/pmem-csi-direct.yaml=deploy/kustomize/kubernetes-base-direct \
    deploy/kubernetes-X.XX/pmem-csi-lvm.yaml=deploy/kustomize/kubernetes-base-lvm \
    deploy/kubernetes-X.XX/pmem-csi-direct-testing.yaml=deploy/kustomize/kubernetes-base-direct-coverage \
    deploy/kubernetes-X.XX/pmem-csi-lvm-testing.yaml=deploy/kustomize/kubernetes-base-lvm-coverage \

# Kubernetes versions derived from kubernetes-base.
#
# Once we drop support for 1.19, all the remaining versions can
# be moved back here and the changes for storage capacity
# tracking can be moved into kubernetes-base and
# kubernetes-1.20 removed.
KUSTOMIZE_KUBERNETES_VERSIONS = \
    1.19 \
    1.20
KUSTOMIZE += $(foreach version,$(KUSTOMIZE_KUBERNETES_VERSIONS),$(subst X.XX,$(version),$(KUSTOMIZE_KUBERNETES_OUTPUT)))

# Deployments that have storage capacity tracking enabled, using the v1beta1 API.
KUSTOMIZE += $(subst kubernetes-base,kubernetes-1.21,$(subst X.XX,1.21,$(KUSTOMIZE_KUBERNETES_OUTPUT)))

KUSTOMIZE += deploy/common/pmem-storageclass-default.yaml=deploy/kustomize/storageclass
KUSTOMIZE += deploy/common/pmem-storageclass-ext4.yaml=deploy/kustomize/storageclass-ext4
KUSTOMIZE += deploy/common/pmem-storageclass-xfs.yaml=deploy/kustomize/storageclass-xfs
KUSTOMIZE += deploy/common/pmem-storageclass-late-binding.yaml=deploy/kustomize/storageclass-late-binding
KUSTOMIZE += deploy/operator/pmem-csi-operator.yaml=deploy/kustomize/operator

KUSTOMIZE_OUTPUT := $(foreach item,$(KUSTOMIZE),$(firstword $(subst =, ,$(item))))

# This function takes the name of a .yaml output file and returns the
# corresponding kustomization as specified in KUSTOMIZE. It works by
# iterating over all items and only return the second half of the
# right one.
KUSTOMIZE_LOOKUP_KUSTOMIZATION = $(strip $(foreach item,$(KUSTOMIZE),$(if $(filter $(1)=%,$(item)),$(word 2,$(subst =, ,$(item))))))

# This function takes the kustomize binary and the name of an output
# file as arguments and returns the command which produces that file
# as stdout.
KUSTOMIZE_INVOCATION = (echo '\# Generated with "make kustomize", do not edit!'; echo; $(1) build --load-restrictor LoadRestrictionsNone $(call KUSTOMIZE_LOOKUP_KUSTOMIZATION,$(2)))

$(KUSTOMIZE_OUTPUT): _work/kustomize $(KUSTOMIZE_INPUT)
	mkdir -p ${@D}
	$(call KUSTOMIZE_INVOCATION,$<,$@) >$@
	if echo "$@" | grep '/pmem-csi-' | grep -qv '\-operator'; then \
		dir=$$(echo "$@" | tr - / | sed -e 's;kubernetes/;kubernetes-;' -e 's;/alpha/;-alpha/;'  -e 's;/distributed/;-distributed/;' -e 's/.yaml//' -e 's;/pmem/csi/;/;') && \
		mkdir -p $$dir && \
		cp $@ $$dir/pmem-csi.yaml && \
		echo 'resources: [ pmem-csi.yaml ]' > $$dir/kustomization.yaml; \
	fi

kustomize: _work/go-bindata clean_kustomize_output $(KUSTOMIZE_OUTPUT)
	$< -o deploy/bindata_generated.go -pkg deploy deploy/kubernetes-*/*/pmem-csi.yaml deploy/kustomize/webhook/webhook.yaml deploy/kustomize/scheduler/scheduler-service.yaml

clean_kustomize_output:
	rm -rf deploy/kubernetes-*
	rm -f $(KUSTOMIZE_OUTPUT)

# Always re-generate the output files because "git rebase" might have
# left us with an inconsistent state.
.PHONY: kustomize $(KUSTOMIZE_OUTPUT)

.PHONY: clean-kustomize
clean: clean-kustomize
clean-kustomize:
	rm -f _work/kustomize-*
	rm -f _work/kustomize

.PHONY: clean-go-bindata
clean: clean-go-bindata
clean-go-bindata:
	rm -f _work/go-bindata
_work/go-bindata:
	$(GO_BINARY) build -o $@ github.com/go-bindata/go-bindata/go-bindata

.PHONY: test-kustomize $(addprefix test-kustomize-,$(KUSTOMIZE_OUTPUT))
test: test-kustomize
test-kustomize: $(addprefix test-kustomize-,$(KUSTOMIZE_OUTPUT))
$(addprefix test-kustomize-,$(KUSTOMIZE_OUTPUT)): test-kustomize-%: _work/kustomize
	@ if ! diff <($(call KUSTOMIZE_INVOCATION,$<,$*)) $*; then echo "$* was modified manually" && false; fi

# Targets in the makefile can depend on check-go-version-<path to go binary>
# to trigger a warning if the x.y version of that binary does not match
# what the project uses. Make ensures that this is only checked once per
# invocation.
.PHONY: check-go-version-%
check-go-version-%:
	@ hack/verify-go-version.sh "$*"

SPHINXOPTS    = -W --keep-going # Warn about everything, abort with an error at the end.
SPHINXBUILD   = sphinx-build
SOURCEDIR     = .
BUILDDIR      = _output

# Generate doc site under _build/html with Sphinx.
# "vhtml" will set up tools, "html" expects them to be installed.
# GITHUB_SHA will be used for kustomize references to the GitHub
# repo (= github.com/intel/pmem-csi/deploy, a syntax that is only
# valid there) if set.
GEN_DOCS = $(SPHINXBUILD) -M html "$(SOURCEDIR)" "$(BUILDDIR)" $(SPHINXOPTS) $(O) && \
	( ! [ "$$GITHUB_SHA" ] || ! [ "$$GITHUB_REPOSITORY" ] || \
	  find $(BUILDDIR)/html/ -name '*.html' | \
	  xargs sed -i -e "s;github.com/intel/pmem-csi/\\(deploy/\\S*\\);github.com/$$GITHUB_REPOSITORY/\\1?ref=$$GITHUB_SHA;g" ) && \
	cp docs/html/index.html $(BUILDDIR)/html/index.html && cp docs/js/copybutton.js $(BUILDDIR)/html/_static/copybutton.js
vhtml: _work/venv/.stamp
	. _work/venv/bin/activate && $(GEN_DOCS)
html:
	$(GEN_DOCS)

clean-html:
	rm -rf _output/html

# Set up a Python3 environment with the necessary tools for document creation.
_work/venv/.stamp: docs/requirements.txt
	rm -rf ${@D}
	python3 -m venv ${@D}
	. ${@D}/bin/activate && pip install -r $<
	touch $@
