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
             [ "$( (git diff "${GIT_BRANCH}..HEAD" -- pkg/apis Dockerfile;
                        git diff "${}..HEAD" | grep -e '^@@.*@@ import (' -e '^[+-]import') |
		           wc -l)" -eq 0 ] ); then
    echo "Skipping generated code check because the CI job does not affect it."
elif ! (set -x; make generate); then
    echo "ERROR: code generation failed."
    exit 1
elif [ "$(git status --porcelain -- $(find . -name '*generated*' | grep -v -e ^./vendor -e ^./hack) | wc -l)" -gt 0 ]; then
    echo "ERROR: generated files *not* up-to-date, they did get modified by 'make generate':";
    git diff -- $(find . -name '*generated*' | grep -v -e ^./vendor -e ^./hack)
    exit 1
else
    echo "Generated code is up-to-date."
fi
