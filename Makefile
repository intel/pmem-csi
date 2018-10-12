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

REGISTRY_NAME=localhost:5000
IMAGE_VERSION_pmem-csi-driver=canary
IMAGE_VERSION_pmem-ns-init=canary
IMAGE_VERSION_pmem-vgm=canary
IMAGE_TAG=$(REGISTRY_NAME)/$*:$(IMAGE_VERSION_$*)
IMAGE_BUILD_ARGS=
BUILD_ARGS=--build-arg http_proxy=${http_proxy} --build-arg https_proxy=${https_proxy} --build-arg no_proxy=${no_proxy}

all: pmem-csi-driver pmem-ns-init pmem-vgm

build-images: build-pmem-csi-driver-image build-pmem-ns-init-image build-pmem-vgm-image

push-images: push-pmem-csi-driver-image push-pmem-ns-init-image push-pmem-vgm-image

test:
	go test $(IMPORT_PATH)/pkg/... -cover
	go vet $(IMPORT_PATH)/pkg/...

pmem-csi-driver pmem-vgm pmem-ns-init:
	GOOS=linux go build -a -o _output/$@ ./cmd/$@

build-%-image:
	docker build ${BUILD_ARGS} ${IMAGE_BUILD_ARGS} -t $(IMAGE_TAG) -f ./cmd/$*/Dockerfile .

push-%-image: build-%-image
	docker push $(IMAGE_TAG)

clean:
	go clean -r -x
	-rm -rf _output

.PHONY: all test clean pmem-csi-driver pmem-ns-init pmem-vgm
