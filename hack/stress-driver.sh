# !/bin/bash

# Utility script to stress the driver by creating and deleting the volumes. It
# runs by default 100 runs, each run it creates and deletes 10(5 xfs and 5 ext)
# volumes of each 4GB size. Hence, it requires approximately 40 GB of PMEM memory
# 
# The main intention of this script is to determine the default resource requirements
# to be set for the driver deployed via the operator.
# This is expected to run by setting up the metrics-server and VirtualPodAutoscaler
# describe in the 'Performance and resource measurements' section in the
# developer documentation(DEPLOYMENT.md).
#
# NOTE: This script is *not* expected to run on real clusters where the user
# has exisitng PV/PVCs.

ROUNDS=${ROUNDS:-100}
VOL_COUNT=${VOL_COUNT:-5} # 5 ext4 + 5  xfs = 10 * 4Gi ~= 40Gi

for i in $(seq 1 1 $ROUNDS) ; do
  echo "Round #$i:"
  echo "Creating volumes..."
  for j in  $(seq 1 1 $VOL_COUNT) ; do
    sed -e "s;\(.*name:\)\(.*\);\1\2-$j;g" < deploy/common/pmem-pvc.yaml | kubectl create -f -
  done
  echo "Deleting all pvc..."
  while [ "$(kubectl get pv --no-headers | wc -l)" -ne "0" ]; do
    kubectl delete pvc --all
    kubectl delete pv --all
  done
done
