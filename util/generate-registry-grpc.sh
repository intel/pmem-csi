#!/bin/bash
#prerequisites:
# protoc - protocol buffer compiler
# gogo-protobuf - Go protobuf generator
pushd ./pkg/pmem-registry
protoc --gogo_out=plugins=grpc:. pmem-registry.proto
popd
