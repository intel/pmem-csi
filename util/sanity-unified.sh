#!/bin/bash -xeu

# run API testing using csi-sanity in singlehost, drivermode=Unified

$GOPATH/bin/csi-sanity --csi.endpoint 127.0.0.1:10000 -csi.testvolumesize 512000000 \
  -csi.mountdir /tmp/csimount -csi.stagingdir /tmp/csistage
