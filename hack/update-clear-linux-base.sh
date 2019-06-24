#!/bin/sh
#
# Copyright 2019 Intel Corporation.
#
# SPDX-License-Identifier: Apache-2.0
#
# Invoke this script on a system that has Docker installed
# such that it can be used by the current user. Then the script
# will bump up the CLEAR_LINUX_BASE and SWUPD_UPDATE_ARG
# parameters in the Dockerfile such that they pick the
# current version of Clear Linux.

die () {
    echo "ERROR: $@"
    exit 1
}

docker image pull clearlinux:latest || die "pulling clearlinux:latest failed"
base=$(docker inspect --format='{{index .RepoDigests 0}}' clearlinux:latest) || die "failed to inspect clearlinux:latest"
echo "Base image: $base"

# We rely on swupd to determine what this particular image can be
# updated to with "swupd update --version". This might not be the very latest
# Clear Linux, for example when there has been a format bump and the
# base image is still using the older format.
output=$(docker run $base swupd check-update) || die "failed to obtain information about available updates"

# The expected output is:
# Current OS version: 29940
# Latest server version: 29970
# There is a new OS version available: 29970
version=$(echo "$output" | tail -n 1 | sed -e 's/.*: *//')
echo "Update version: $version"

# Do a trial-run with these parameters.
docker run "$base" swupd update --version=$version || die "failed to update"

# Now update the Dockerfile.
sed -i -e 's;^\(ARG CLEAR_LINUX_BASE=\).*;\1'"$base"';' -e 's;^\(ARG SWUPD_UPDATE_ARG=\).*;\1"--version='"$version"'";' Dockerfile || die "failed to patch Dockerfile"

# Show the modification
echo "Done."
git status Dockerfile
git diff Dockerfile
