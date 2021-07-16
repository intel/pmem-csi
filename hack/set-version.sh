#!/bin/sh -e
#
# Copyright 2019 Intel Corporation.
#
# SPDX-License-Identifier: Apache-2.0
#
# Invoke this script with a version as parameter
# and it will update all hard-coded image versions
# in the source code.

if [ $# != 2 ] || [ "$1" = "?" ] || [ "$1" = "--help" ]; then
    echo "Usage: $0 <previous version> <new version> (version with v prefix, e.g. v0.9.0)" >&2
    exit 1
fi

old_version=$1
new_version=$2

set -x

sed -i -e "s;\(IMAGE_VERSION?*=\|intel/pmem-[^ ]*:\)[^ ^;/]*;\1${new_version};g" $(git grep -l 'IMAGE_VERSION?*=\|intel/pmem-[^ ]*:' Makefile test/*.sh deploy)
sed -i -e "s;/pmem-csi-driver\([^ ]*\):[^ {}]*;/pmem-csi-driver\1:${new_version};g" test/test-config.sh docs/*.md
sed -i -e "s;\(TEST_PMEM_IMAGE_TAG:=\)canary;\1${new_version};g" test/test-config.sh
sed -i -e "s;github.com/intel/pmem-csi/raw/[^/]*/;github.com/intel/pmem-csi/raw/${new_version}/;g" docs/*.md
sed -i -e "s;LABEL version=\".*\";LABEL version=\"${new_version}\";" Dockerfile.UBI
