# Redis-pmem operator

This readme describes a complete example to deploy a Redis cluster through the [redis-operator](https://github.com/spotahome/redis-operator) using QEMU-emulated persistent memory devices.

## Prerequisites
* Docker-ce: version 18.06.1
* [Docker registry](https://docs.docker.com/registry/deploying/).

## PMEM-CSI installation
The steps below describe how to install PMEM-CSI within a Kubernetes cluster that contains one master and three worker nodes. We use a specific version of [Clear Linux OS](https://clearlinux.org/) for the nodes, so the following steps can be reproduced.
1. Clone this project into `$GOPATH/src/github.com/intel`.
2. Build PMEM-CSI:
    ``` console
     $ cd $GOPATH/src/github.com/intel/pmem-csi
     $ make push-images
    ```
    **Note**: By default, the build images are pushed into a local [Docker registry](https://docs.docker.com/registry/deploying/).

3. Start the Kubernetes cluster:
    ``` console
    $ TEST_CLEAR_LINUX_VERSION=29820 make start
    ```
    **WARNING**: You may run into an SSL problem when executing this command. You can avoid the SSL error by disabling test checks for signed files, however, be aware that this is not a secure step. Use this command to disable checking for signed files: `TEST_CLEAR_LINUX_VERSION=29820 TEST_CHECK_SIGNED_FILES=false make start`.

4. Setup `KUBECONFIG` env variable to use `kubectl` binary:
    ``` console
    $ export KUBECONFIG=$(pwd)/_work/clear-govm/kube.config
    ```
5. Verify that all pods reach `Running` status:
    ``` console
    $ kubectl get po
    NAME                          READY   STATUS    RESTARTS   AGE
    pmem-csi-controller-0         3/3     Running   0          125ms
    pmem-csi-node-c82p5           2/2     Running   0          124m
    pmem-csi-node-dwws2           2/2     Running   0          125m
    pmem-csi-node-kj7gs           2/2     Running   0          125m
    ```

## Redis operator installation and Redis cluster deployment
The steps to install the Redis operator by Spotahome are listed below:
 1. Install the Redis operator and validate that the `CRD` was properly defined:
    ``` console
    $ kubectl create -f https://raw.githubusercontent.com/spotahome/redis-operator/master/example/operator/all-redis-operator-resources.yaml

    $ kubectl get po
    NAME                             READY   STATUS    RESTARTS   AGE
    pmem-csi-controller-0            3/3     Running   0          134m
    pmem-csi-node-c82p5              2/2     Running   0          133m
    pmem-csi-node-dwws2              2/2     Running   0          134m
    pmem-csi-node-kj7gs              2/2     Running   0          134m
    redisoperator-688f6f4fcc-lv5h5   1/1     Running   0          7m29s

    $ kubectl get crd
    NAME                                     CREATED AT
    csidrivers.csi.storage.k8s.io            2019-06-21T16:55:46Z
    csinodeinfos.csi.storage.k8s.io          2019-06-21T16:55:47Z
    redisfailovers.databases.spotahome.com   2019-06-21T19:03:36Z
    ```
2. Deploy a Redis cluster that uses PMEM-CSI:
    ``` console
    $ kubectl create -f https://raw.githubusercontent.com/spotahome/redis-operator/master/example/redisfailover/pmem.yaml

    $ kubectl get po
    NAME                                      READY   STATUS    RESTARTS   AGE
    pmem-csi-controller-0                     3/3     Running   0          145m
    pmem-csi-node-c82p5                       2/2     Running   0          144m
    pmem-csi-node-dwws2                       2/2     Running   0          145m
    pmem-csi-node-kj7gs                       2/2     Running   0          145m
    redisoperator-688f6f4fcc-lv5h5            1/1     Running   0          18m
    rfr-redisfailover-pmem-0                  1/1     Running   0          9m37s
    rfr-redisfailover-pmem-1                  1/1     Running   0          6m28s
    rfr-redisfailover-pmem-2                  1/1     Running   0          3m15s
    rfs-redisfailover-pmem-7595857d4c-56mmz   1/1     Running   0          9m37s
    rfs-redisfailover-pmem-7595857d4c-c69xg   1/1     Running   0          9m37s
    rfs-redisfailover-pmem-7595857d4c-cqx4p   1/1     Running   0          9m37s
    ```
    **Note**: These steps deploy a Redis cluster with three Redis and three sentinel instances as it is shown when executing `kubectl get po` command. The provided persistent volumes use the specifications in the `persistentVolumeClaim` section of `example/redisfailover/pmem.yaml`, i.e.: 100Mi of storage. Feel free to play around with the numbers of instances to be deployed at `example/redisfailover/pmem.yaml`.

## Verify that the Redis instances are using the volumes provided
First, match the volumes claim name used by each Redis instance to the current `pvc` which is provisioned by `pmem-csi-sc-ext4`. Both items must be properly bound.
Next, check the block devices in the worker nodes of your Kubernetes cluster and match the ones under the pmem `block` device, as shown below.
``` console
$ kubectl get pvc
NAME                                               STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS       AGE
redisfailover-pmem-data-rfr-redisfailover-pmem-0   Bound    pvc-82ee55fc-9458-11e9-bec2-0242ac110004   100Mi      RWO            pmem-csi-sc-ext4   19m
redisfailover-pmem-data-rfr-redisfailover-pmem-1   Bound    pvc-f3a04c91-9458-11e9-bec2-0242ac110004   100Mi      RWO            pmem-csi-sc-ext4   16m
redisfailover-pmem-data-rfr-redisfailover-pmem-2   Bound    pvc-668ec3c6-9459-11e9-bec2-0242ac110004   100Mi      RWO            pmem-csi-sc-ext4   13m

$ kubectl exec rfr-redisfailover-pmem-0 -- df /data
Filesystem                                                   1K-blocks  Used Available Use% Mounted on
/dev/ndbus0region0fsdax/8999c281-9458-11e9-8866-a611eaeb0cd9     95088    72     87848   1% /data

$ kubectl exec rfr-redisfailover-pmem-0 -- mount | grep /data
/dev/ndbus0region0fsdax/8999c281-9458-11e9-8866-a611eaeb0cd9 on /data type ext4 (rw,relatime,dax,stripe=256)
```

## Playing around with Redis operator
The steps to start playing around with Redis cluster *through sentinel instances* managed by the operator, are listed below:
1. Get the network information of the Redis node working as a master. You can use any sentinel instance, i.e.:
    ``` console
    $ kubectl exec -it rfs-redisfailover-pmem-7595857d4c-56mmz -- redis-cli -p 26379 SENTINEL get-master-addr-by-name mymaster
    1) "10.244.3.4"
    2) "6379"
    ```
2. Set a `key:value` pair into Redis master:
    ``` console
    $ kubectl exec -it rfs-redisfailover-pmem-7595857d4c-c69xg -- redis-cli -h 10.244.3.4 -p 6379 SET hello world!
    OK
    ```
3. Get `value` from `key`:
    ``` console
    $ kubectl exec -it rfs-redisfailover-pmem-7595857d4c-c69xg -- redis-cli -h 10.244.3.4 -p 6379 GET hello
    "world!"
    ```
4. Feel free to play around with the Redis cluster.

## High availability for Redis instances
In order to ensure high availability for Redis instances, it's required to use `hardAntiAffinity: True` and `storageClassName: pmem-csi-sc-late-binding` under Redis deployment specification, i.e.:
```
apiVersion: databases.spotahome.com/v1
kind: RedisFailover
metadata:
  name: redisfailover-pmem-lb
spec:
  sentinel:
    replicas: 3
    ...
  redis:
    hardAntiAffinity: True
    replicas: 3
    ...
    storage:
      persistentVolumeClaim:
        metadata:
          name: redisfailover-pmem-data
        spec:
          accessModes:
            - ReadWriteOnce
          resources:
            requests:
              storage: 100Mi
          storageClassName: pmem-csi-sc-late-binding
```

The deployment can be done in the same way as mentioned before, i.e.:
``` console
$ kubectl create -f /path/to/pmem-lb.yaml
redisfailover.databases.spotahome.com/redisfailover-pmem-lb created

$ kubectl get po -o custom-columns=NAME:.metadata.name,STATUS:.status.phase,NODE:.spec.nodeName,IP:.status.podIP
NAME                                         STATUS    NODE                          IP
pmem-csi-controller-0                        Running   pmem-csi-clear-govm-worker3   10.244.3.2
pmem-csi-node-l8d58                          Running   pmem-csi-clear-govm-worker3   172.17.0.5
pmem-csi-node-v8cb4                          Running   pmem-csi-clear-govm-worker2   172.17.0.4
pmem-csi-node-xwk2l                          Running   pmem-csi-clear-govm-worker1   172.17.0.3
redisoperator-688f6f4fcc-l6p4f               Running   pmem-csi-clear-govm-worker1   10.244.2.2
rfr-redisfailover-pmem-lb-0                  Running   pmem-csi-clear-govm-worker2   10.244.1.3
rfr-redisfailover-pmem-lb-1                  Running   pmem-csi-clear-govm-worker1   10.244.2.4
rfr-redisfailover-pmem-lb-2                  Running   pmem-csi-clear-govm-worker3   10.244.3.4
rfs-redisfailover-pmem-lb-5c455d7974-9twjz   Running   pmem-csi-clear-govm-worker1   10.244.2.3
rfs-redisfailover-pmem-lb-5c455d7974-dscmx   Running   pmem-csi-clear-govm-worker2   10.244.1.2
rfs-redisfailover-pmem-lb-5c455d7974-jtznn   Running   pmem-csi-clear-govm-worker3   10.244.3.3
```
As it is shown, each `rfr-redisfailover-pmem-lb-X` gets assigned to a different Kubernetes cluster node `pmem-csi-clear-govm-workerX`, where `X` &isin; {1,2,3}.