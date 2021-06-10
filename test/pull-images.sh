#!/bin/bash

set -o errexit

TEST_DIRECTORY=${TEST_DIRECTORY:-$(dirname $(readlink -f $0))}
source ${TEST_CONFIG:-${TEST_DIRECTORY}/test-config.sh}

CLUSTER=${CLUSTER:-pmem-govm}
REPO_DIRECTORY="${REPO_DIRECTORY:-$(dirname $(dirname $(readlink -f $0)))}"
CLUSTER_DIRECTORY="${CLUSTER_DIRECTORY:-${REPO_DIRECTORY}/_work/${CLUSTER}}"
SSH="${CLUSTER_DIRECTORY}/ssh.0"
KUBECTL="${SSH} kubectl" # Always use the kubectl installed in the cluster.

$KUBECTL apply -f - <<EOF
kind: DaemonSet
apiVersion: apps/v1
metadata:
  name: pmem-csi-intel-com-image-pull
  namespace: default
  labels:
    app.kubernetes.io/name: pmem-csi-image-pull
    app.kubernetes.io/part-of: pmem-csi
    app.kubernetes.io/component: image-pull
    app.kubernetes.io/instance: dev.pmem-csi.intel.com
spec:
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 10000 # pull on all nodes at the same time
  selector:
    matchLabels:
        app.kubernetes.io/name: pmem-csi-image-pull
        app.kubernetes.io/instance: dev.pmem-csi.intel.com
  template:
    metadata:
      labels:
        app.kubernetes.io/name: pmem-csi-image-pull
        app.kubernetes.io/part-of: pmem-csi
        app.kubernetes.io/component: image-pull
        app.kubernetes.io/instance: dev.pmem-csi.intel.com
        pmem-csi.intel.com/webhook: ignore
      annotations:
        # Updating the annotations each time the script is invoked
        # ensures that all pods get recreated, which then will pull
        # all images again.
        pull-requested-at: "$(date)"
    spec:
      # Allow this pod to run on all nodes and
      # prevent eviction (https://github.com/kubernetes-csi/csi-driver-host-path/issues/47#issuecomment-538469081).
      tolerations:
      - effect: NoSchedule
        operator: Exists
      - effect: NoExecute
        operator: Exists
      priorityClassName: system-node-critical
      containers:
      - name: pmem-csi-driver
        image: ${TEST_PMEM_REGISTRY}/pmem-csi-driver:canary
        imagePullPolicy: Always
        command:
        - sleep
        - "1000000000"
        resources:
          requests:
            memory: 250Mi
            cpu: 100m
      - name: pmem-csi-driver-test
        image: ${TEST_PMEM_REGISTRY}/pmem-csi-driver-test:canary
        imagePullPolicy: Always
        command:
        - sleep
        - "1000000000"
        resources:
          requests:
            memory: 250Mi
            cpu: 100m

EOF
