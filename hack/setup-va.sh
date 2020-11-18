#!/bin/sh
#
# Copyright 2020 Intel Corporation

set -ex

if ! [ -d _work/autoscaler ]; then
    git clone git@github.com:kubernetes/autoscaler.git _work/autoscaler
fi
cd _work/autoscaler
git fetch origin
git checkout vertical-pod-autoscaler-0.9.0

cd vertical-pod-autoscaler
hack/vpa-up.sh
