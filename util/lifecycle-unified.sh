#!/bin/bash -xeu

# run simple volume lifecycle in singlehost, drivermode=Unified
# VOLUME_ID is parsed from output of create command

export CSI_ENDPOINT=tcp://127.0.0.1:10000
NAME=nspace5
STAGEPATH=/tmp/stage5
TARGETPATH=/tmp/target5
SIZE=33554432
MODE=xfs
out=`$GOPATH/bin/csc controller create-volume --req-bytes $SIZE --cap SINGLE_NODE_WRITER,block,$MODE --params eraseafter=false $NAME`
VOLID=`echo $out |awk '{print $1}' |tr -d \"`
#echo [$VOLID]
mkdir -p $STAGEPATH
$GOPATH/bin/csc node stage $VOLID --cap SINGLE_NODE_WRITER,mount,$MODE --staging-target-path $STAGEPATH --attrib name=$NAME
$GOPATH/bin/csc node publish $VOLID --cap SINGLE_NODE_WRITER,mount,$MODE --staging-target-path $STAGEPATH --target-path $TARGETPATH
$GOPATH/bin/csc node unpublish $VOLID --target-path $TARGETPATH
$GOPATH/bin/csc node unstage $VOLID --staging-target-path $STAGEPATH
$GOPATH/bin/csc controller delete-volume $VOLID
