#!/bin/sh
#
# Copyright 2020 Intel Corporation.
#
# SPDX-License-Identifier: Apache-2.0
#
# Lists all direct dependencies of code in a certain module.
#
# Usage: <module> <package> <package> ...
# Example: github.com/intel/pmem-csi ./cmd/pmem-csi-driver

PACKAGE="$1"
shift

: ${GO:=go}

# Normalize package so that they start with the module.
IMPORTS=$(env $GO list "$@")

# Repeatedly expand the list of imports that come from the package(s)
# until we find no new ones, then print the direct imports of those.
while true; do
    NEW_IMPORTS=$( (echo "$IMPORTS"; env $GO list -f '{{join .Imports "\n"}}' $(echo "$IMPORTS" | grep "^$PACKAGE") ) | sort -u)
    if [ "$NEW_IMPORTS" != "$IMPORTS" ]; then
        IMPORTS="$NEW_IMPORTS"
    else
        break
    fi
done

env $GO list -f '{{join .Imports "\n"}}' $(echo "$IMPORTS" | grep "^$PACKAGE") | sort -u
