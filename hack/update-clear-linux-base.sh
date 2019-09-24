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
#
# If the version bump leads to relevant changes in the content
# of the images, then the updated Dockerfile is committed.

die () {
    echo "ERROR: $@"
    exit 1
}

# Sanity check: ensure that current Dockerfile is unmodified.
git diff --exit-code Dockerfile || die "Invoke this script in a clean pmem-csi repository."

# If invoked on a branch where the base is not locked down, we skip the
# comparison because we always want to lock down.
oldversion=$(grep '^ARG SWUPD_UPDATE_ARG="--version=' Dockerfile | sed -e 's/.*=\(.*\)"/\1/')
if [ "$oldversion" != "latest" ]; then
    # Build the two kinds of images which are derived from Clear Linux. We
    # compare against those later. CACHEBUST is intentionally not set because
    # this script is intended to be invoked on a release branch where the base
    # is locked, so we don't need to rebuild if the image is already cached.
    docker build --target build -t pmem-csi-build . || die "failed to build the 'build' image"
    docker build --target runtime -t pmem-csi-runtime . || "failed to build the 'runtime' image"
fi

docker image pull clearlinux:latest || die "pulling clearlinux:latest failed"
base=$(docker inspect --format='{{index .RepoDigests 0}}' clearlinux:latest) || die "failed to inspect clearlinux:latest"
echo "Base image: $base"

# We rely on swupd to determine what this particular image can be
# updated to with "swupd update --version". This might not be the very latest
# Clear Linux, for example when there has been a format bump and the
# base image is still using the older format.
output=$(docker run $base swupd check-update) # will return non-zero exit code if there is nothing to update
# The expected output on failure is one of:
#     Current OS version: 30450
#     Latest server version: 30450
#     There are no updates available
# or:
#     Current OS version: 29940
#     Latest server version: 29970
#     There is a new OS version available: 29970
version=$(echo "$output" | grep "Latest server version" | tail -n 1 | sed -e 's/.*: *//')
if [ ! "$version" ]; then
    die "failed to obtain information about available updates"
fi
echo "Update version: $version"

# Do a trial-run with these parameters.
docker run "$base" swupd update --version=$version || die "failed to update"

# Now update the Dockerfile.
sed -i -e 's;^\(ARG CLEAR_LINUX_BASE=\).*;\1'"$base"';' -e 's;^\(ARG SWUPD_UPDATE_ARG=\).*;\1"--version='"$version"'";' Dockerfile || die "failed to patch Dockerfile"

# Show the modification
if git diff --exit-code Dockerfile; then
    echo "No new version, exiting now."
    exit 0
fi

no_image_changes () (
    image="$1"
    tmpdir=$(mktemp -d)
    container1=
    container2=
    trap 'rm -rf $tmpdir; for id in $container1 $container2; do docker rm $id; done' EXIT

    # We exclude certain irrelevant files by not even unpacking them.
    # We could also delete some of them from the layer in the first place.
    cat >"$tmpdir/exclude" <<EOF
usr/share/locale/**
usr/share/debuginfo/**
usr/share/info/**
usr/share/man/**
usr/share/doc/**
var/cache/man/**
usr/share/package-licenses/**
*.pyc
etc/os-release
lib/os-release
usr/lib/os-release
EOF

    # We only compare file content. uid/gid, timestamp and permission changes
    # could theoretically be the only changes, but that seems unlikely.
    # Should that ever be the only change, we'll still update eventually
    # once a later update changes a file.
    container1=$(docker create $image)
    container2=$(docker create $image-updated)
    mkdir "$tmpdir/$oldversion"
    mkdir "$tmpdir/$version"
    docker export $container1 | tar -C "$tmpdir/$version" -xf - --exclude-from="$tmpdir/exclude"
    docker export $container2 | tar -C "$tmpdir/$oldversion" -xf -  --exclude-from="$tmpdir/exclude"
    diff --brief -r --exclude-from=- "$tmpdir/$oldversion" "$tmpdir/$version" <<EOF
$tmpdir/$oldversion/usr/share/locale/**
EOF
)

if [ "$oldversion" != "latest" ]; then
    # Build images again with updated Dockerfile.
    if ( docker build --target build -t pmem-csi-build-updated . || die "failed to build the 'build' image" ) &&
           no_image_changes pmem-csi-build &&
           ( docker build --target runtime -t pmem-csi-runtime-updated . || die "failed to build the 'runtime' image" ) &&
           no_image_changes pmem-csi-runtime; then
        echo "Nothing relevant has changed in the image content, no need to update."
        git checkout Dockerfile
        exit 0
    fi
fi

git commit -m "Clear Linux $version" Dockerfile || die "failed to commit modified Dockerfile"
echo "Done."
