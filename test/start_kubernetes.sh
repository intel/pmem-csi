#!/bin/sh -e

# Both should be already running, but CRI-O might have failed due
# to https://github.com/clearlinux/distribution/issues/192.
SSH 'systemctl start crio kubelet'

cnt=0
while [ $cnt -lt 60 ]; do
    if SSH kubectl get nodes >/dev/null 2>/dev/null; then
        exit 0
    fi
    cnt=$(expr $cnt + 1)
    sleep 1
done
echo "timed out waiting for Kubernetes"
SSH kubectl get nodes
SSH kubectl get pods --all-namespaces
exit 1
