#!/bin/sh

# Copyright 2019 The Kubernetes Authors.
#
# Derived from https://github.com/kubernetes-csi/csi-release-tools/blob/c8a1c4af933311a7e63765cd2b64ca45a0fb7dba/verify-vendor.sh

# JOB_NAME and CHANGE_ID are standard Jenkins env vars,
# GIT_BRANCH comes from https://wiki.jenkins.io/display/JENKINS/Git+Plugin.
if [ "${JOB_NAME}" ] &&
       ( ! [ "${CHANGE_ID}" ] ||
             # We consider Dockerfile here because that is where we define the Go version
             # used by the CI, which may have an effect.
             [ "$( (git diff "${GIT_BRANCH}..HEAD" -- go.mod go.sum vendor Dockerfile;
                        git diff "${}..HEAD" | grep -e '^@@.*@@ import (' -e '^[+-]import') |
		           wc -l)" -eq 0 ] ); then
    echo "Skipping vendor check because the CI job does not affect dependencies."
elif ! (set -x; env GO111MODULE=on go mod tidy); then
    echo "ERROR: vendor check failed."
    exit 1
elif [ "$(git status --porcelain -- go.mod go.sum | wc -l)" -gt 0 ]; then
    echo "ERROR: go module files *not* up-to-date, they did get modified by 'GO111MODULE=on go mod tidy':";
    git diff -- go.mod go.sum
    exit 1
elif [ -d vendor ]; then
    if ! (set -x; env GO111MODULE=on go mod vendor); then
	echo "ERROR: vendor check failed."
	exit 1
    elif [ "$(git status --porcelain -- vendor | wc -l)" -gt 0 ]; then
	echo "ERROR: vendor directory *not* up-to-date, it did get modified by 'GO111MODULE=on go mod vendor':"
	git status -- vendor
	git diff -- vendor
	exit 1
    else
	echo "Go dependencies and vendor directory up-to-date."
    fi
else
    echo "Go dependencies up-to-date."
fi
