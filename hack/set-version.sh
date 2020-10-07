#!/bin/sh
#
# Copyright 2019 Intel Corporation.
#
# SPDX-License-Identifier: Apache-2.0
#
# Invoke this script with a version as parameter
# and it will update all hard-coded image versions
# in the source code.

if [ $# != 1 ] || [ "$1" = "?" ] || [ "$1" = "--help" ]; then
    echo "Usage: $0 <image version>" >&2
    exit 1
fi

sed -i -e "s;\(IMAGE_VERSION?*=\|intel/pmem-[^ ]*:\)[^ ^;/]*;\1$1;g" $(git grep -l 'IMAGE_VERSION?*=\|intel/pmem-[^ ]*:' Makefile test/*.sh deploy)
sed -i -e "s;/pmem-csi-driver\([^ ]*\):[^ {}]*;/pmem-csi-driver\1:$1;g" test/test-config.sh
sed -i -e "s;github.com/intel/pmem-csi/raw/[^/]*/;github.com/intel/pmem-csi/raw/$1/;g" docs/*.md
