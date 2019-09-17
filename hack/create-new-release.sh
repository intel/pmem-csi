#!/bin/sh
#
# Copyright 2019 Intel Corporation.
#
# SPDX-License-Identifier: Apache-2.0
#
# This script is meant to be called by the CI to refresh the container
# images on a release branch. Preconditions:
# - Docker is usable (needed by update-clear-linux-base.sh)
# - the current user can commit to GitHub

die () {
    echo "ERROR: $@"
    exit 1
}

# Sanity check: ensure that the current branch is unmodified
# and that we have a branch.
git diff --exit-code || die "Invoke this script in a clean pmem-csi repository."
branch=$(git rev-parse --abbrev-ref HEAD)

# Determine current version. We bump the last digit, because the only
# change is in the base image and that is considered bug fixing.
version=$(make print-image-version) || die "Cannot determine image version."
if [ ! "$version" ] || [ "$version" = "canary" ]; then
    die "This script must be invoked on a release branch where the version has been locked."
fi
minor=$(echo "$version" | sed -e 's/.*\.//')
minor=$(expr $minor + 1)
version=$(echo "$version" | sed -e "s/\(.*\.\).*/\1$minor/")

# Update the base image. Might not change anything.
oldrev=$(git rev-parse HEAD)
hack/update-clear-linux-base.sh || die "Failed to update base image."
newrev=$(git rev-parse HEAD)
if [ "$oldrev" = "$newrev" ]; then
    # Special return code for "nothing changed.
    exit 2
fi

# Bump version.
hack/set-version.sh "$version" || die "Failed to set new version $version."

# Commit and tag.
clearversion=$(grep 'SWUPD_UPDATE_ARG="--version' Dockerfile | sed -e 's/.*version=\(.*\)"/\1/')
git commit --file - --all <<EOF
$version

Update to Clear Linux $clearversion.
EOF
git tag --annotate -m "update to Clear Linux $clearversion" "$version"
