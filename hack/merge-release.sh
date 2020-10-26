#!/bin/sh -ex
#
# Copyright 2019 Intel Corporation.
#
# SPDX-License-Identifier: Apache-2.0
#
# Invoke this script will generate a merge commit of the latest
# release branch into the current branch, without actually changing
# any file.
#
# This is valid on the devel branch because our development process
# ensures that bug fixes and features are always applied on the devel
# branch first.
#
# The purpose is to teach "git describe --tags" that the "devel"
# branch is more recent than the latest tagged release from the
# release branch.

git fetch origin
head_tree=$(git show --pretty=format:%T HEAD)
head_commit=$(git rev-parse HEAD)
latest_release=$(git branch -r | grep 'origin/release-[0-9]*\.[0-9]*' | sort -n | tail -n 1)
release_commit=$(git rev-parse ${latest_release})
new_commit=$(git commit-tree ${head_tree} -p ${head_commit} -p ${release_commit} -F -) <<EOF
merge $(echo "${latest_release}" | sed -e 's;origin/;;')

No file gets changed. The only purpose is to teach "git describe --tags" that the
current branch is more recent than the latest tagged release from the
release branch.
EOF

git merge ${new_commit}
