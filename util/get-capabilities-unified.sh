#!/bin/bash -xeu

# Check driver-reported capabilities

export CSI_ENDPOINT=tcp://127.0.0.1:10000
$GOPATH/bin/csc controller get-capabilities
$GOPATH/bin/csc node get-capabilities
