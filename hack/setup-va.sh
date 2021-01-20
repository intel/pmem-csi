#!/bin/sh
#
# Copyright 2020 Intel Corporation

set -ex

if ! [ -d _work/autoscaler ]; then
    git clone https://github.com/kubernetes/autoscaler _work/autoscaler
fi
cd _work/autoscaler
git fetch origin
git checkout vertical-pod-autoscaler-0.9.0

cd vertical-pod-autoscaler
hack/vpa-up.sh
