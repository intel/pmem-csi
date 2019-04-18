#!/bin/bash
# -*- mode: shell-script; indent-tabs-mode: nil; sh-basic-offset: 4; -*-
# ex: ts=8 sw=4 sts=4 et filetype=sh
#
#  start_qemu.sh
#
#  Copyright (c) 2016-2017 Intel Corporation
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in all
# copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.
#

if [ -z "$1" ]; then
    IMAGE=clear.img
else
    IMAGE="$1"
    shift
fi

if [[ "$IMAGE" =~ .xz$ ]]; then
    >&2 echo "File \"$IMAGE\" is still xz compressed. Uncompress it first with \"unxz\""
    exit 1
fi

if [ ! -f "$IMAGE" ]; then
    >&2 echo "Can't find image file \"$IMAGE\""
    exit 1
fi

# Pick up any sequence of digits as number for this virtual machine.
VMN=$(echo $IMAGE | sed -e 's/[^0-9]*\([0-9]*\).*/\1/')
if ! [ "$VMN" ]; then
    VMN=0
fi

# For each base image we also expect or create a data disk.
DATA_IMAGE=$(readlink -f $IMAGE).data
if [ ! -f "$DATA_IMAGE" ]; then
    truncate --size=1G "$DATA_IMAGE"
fi

# Same MAC address as the one used in setup-clear-kvm.sh.
mac=DE:AD:BE:EF:01:0$VMN

. $(dirname $0)/../../test/test-config.sh

# We must exec here to ensure that our caller can kill qemu by killing its child process.
# The source of entropy for the guest is intentionally the non-blocking /dev/urandom.
# This is sufficient for guests that don't do anything important and avoids draining
# the host of entropy, which happens when using /dev/random and many guests. When that
# happens, guests get stuck during booting.
exec qemu-system-x86_64 \
    -enable-kvm \
    -bios OVMF.fd \
    -smp sockets=1,cpus=4,cores=2 -cpu host \
    -m 2048 \
    -vga none -nographic \
    -object rng-random,filename=/dev/urandom,id=rng0 \
    -device virtio-rng-pci,rng=rng0 \
    -drive file="$IMAGE",if=none,aio=threads,id=disk \
    -device virtio-blk-pci,drive=disk,bootindex=0 \
    -drive file="$DATA_IMAGE",if=none,aio=threads,id=data,format=raw \
    -device virtio-blk-pci,drive=data \
    -netdev tap,ifname=${TEST_PREFIX}tap$VMN,script=no,downscript=no,id=mynet0 \
    -device virtio-net-pci,netdev=mynet0,mac=$mac \
    -debugcon "file:$IMAGE.debug.log" -global isa-debugcon.iobase=0x402 \
    "$@"
