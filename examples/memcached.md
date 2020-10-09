# memcached

As shown in [this blog
post](https://memcached.org/blog/persistent-memory/), memcached can
efficiently utilize PMEM as replacement for DRAM. This makes it
possible to increase cache sizes and/or reduce costs. The memcached
maintainers make container images available which support this
feature. This example shows how to take advantage of PMEM with
memcached running on Kubernetes.

## Prerequisites

The instructions below assume that:
- `kubectl` is available and can access the cluster.
- PMEM-CSI [was installed](/docs/install.md#installation-and-setup)
  with the default `pmem-csi.intel.com` driver name and with the
  [`pmem-csi-sc-ext4` storage
  class](/deploy/common/pmem-storageclass-ext4.yaml).
- `telnet` or similar tools like `socat` are available.

It is not necessary to check out the PMEM-CSI source code.

The next two sections use plain YAML files to install memcached. The
last sections shows installation via the [KubeDB
operator](https://kubedb.com). Additional requirements for that:
- Helm >= 3.0.0
- git (at least until KubeDB 0.14.0 is released)

## PMEM as DRAM replacement

Memcached can map PMEM into its address space and then store the bulk
of the cache there. The advantage is higher capacity and (depending on
pricing) less costs.

The cache could be stored in a persistent volume and reused after a
restart (see next section), but when that isn't the goal, a
[deployment with one CSI ephemeral inline
volume](/deploy/kustomize/memcached/ephemeral/memcached-ephemeral.yaml)
per memcached pod is very simple:
- A Kubernetes deployment manages the memcached instance(s).
- In the pod template, one additional PMEM volume of a certain size is
  requested.
- An init container prepares that volume for use by memcached:
  - It determines how large the actual file can be that memcached will use.
  - It changes volume ownership so that memcached can run as non-root
    process.
- A wrapper script adds the `--memory-file` and `--memory-limit` parameters
  when invoking memcached, which enables the use of PMEM.

In this mode, the PMEM-CSI driver is referenced through its name
(usually `pmem-csi.intel.com`). No storage classes are needed. That
name and other parameters in the deployment can be modified with
[`kustomize`](https://github.com/kubernetes-sigs/kustomize). Here's
how one can change the namespace, volume size or add additional
command line parameters:

``` ShellSession
$ mkdir -p my-memcached-deployment

$ cat >my-memcached-deployment/kustomization.yaml <<EOF
namespace: demo
bases:
 - github.com/intel/pmem-csi/deploy/kustomize/memcached/ephemeral
patchesStrategicMerge:
- update-deployment.yaml
EOF

$ cat >my-memcached-deployment/update-deployment.yaml <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pmem-memcached
spec:
  template:
    spec:
      containers:
      - name: memcached
        env:
        - name: MEMCACHED_ARGS
          value: --conn-limit=500
      volumes:
      - name: data-volume
        csi:
          volumeAttributes:
            size: 500Mi
EOF
```

These commands then create the namespace and the deployment:
``` console
$ kubectl create ns demo
namespace/demo created
$ kubectl create --kustomize my-memcached-deployment
service/pmem-memcached created
deployment.apps/pmem-memcached created
```

Eventually, there will be one pod with memcached running:
``` console
$ kubectl wait --for=condition=Ready -n demo pods -l app.kubernetes.io/name=pmem-memcached
pod/pmem-memcached-96f6b5986-hdx6n condition met
```

How to access a service from remote varies between cluster
installations and how they handle incoming connections. Therefore we
use [`kubectl
port-forward`](https://kubernetes.io/docs/tasks/access-application-cluster/port-forward-access-application-cluster/)
to access the service. Run this command in one shell and keep it
running there:
``` console
$ kubectl port-forward -n demo service/pmem-memcached 11211
Forwarding from 127.0.0.1:11211 -> 11211
Forwarding from [::1]:11211 -> 11211
```

In another shell we can now use `telnet` to connect to memcached:
``` console
$ telnet localhost 11211
Trying ::1...
Connected to localhost.
Escape character is '^]'.
```

Memcached accepts simple plain-text commands. To set a key with 10
characters, a time-to-live of 500 seconds and default flags, enter:
```
set demo_key 0 500000 10
I am PMEM.
```

Memcached acknowledges this with:
```
STORED
```

We can verify that the key exists with:
```
get demo_key
```

```
VALUE demo_key 0 10
I am PMEM.
END
```

To disconnect, use:
```
quit
```

```
Connection closed by foreign host.
```

The following command verifies the data was stored in a persistent
memory data volume:
``` console
$ kubectl exec -n demo $(kubectl get -n demo pods -l app.kubernetes.io/name=pmem-memcached -o jsonpath={..metadata.name}) grep 'I am PMEM.' /data/memcached-memory-file
Binary file /data/memcached-memory-file matches
```

To clean up, terminate the `kubectl port-forward` command and delete the memcached deployment with:
``` console
$ kubectl delete --kustomize my-memcached-deployment
service "pmem-memcached" deleted
deployment.apps "pmem-memcached" deleted
```



## Restartable Cache

The [restartable cache
feature](https://github.com/memcached/memcached/wiki/WarmRestart)
works like non-persistent usage of PMEM. In addition, memcached writes
out one additional, short state file during a shutdown triggered by
`SIGUSR1`. The state file describes the content of the memory file and
is used during a restart by memcached to decide whether it can use the
existing cached data.

The [example
deployment](/deploy/kustomize/memcached/persistent/memcached-persistent.yaml)
uses a stateful set because that can automatically create persistent
volumes for each instance. The shell wrapper around memcached
translates the normal [`SIGTERM` shutdown
signal](https://kubernetes.io/docs/concepts/workloads/pods/pod/#termination-of-pods)
into `SIGUSR1`.

Deploying like that becomes less flexible because the memcached pods
can no longer move freely between nodes. Instead, each pod has to be
restarted on the node where its volume was created. There are also
[several caveats for memcached in this
mode](https://github.com/memcached/memcached/wiki/WarmRestart#caveats)
that admins and application developers must be aware of.

This example can also be kustomized. It uses the [`pmem-csi-sc-ext4`
storage class](/deploy/common/pmem-storageclass-ext4.yaml). Here we
just use the defaults, in particular the default namespace:

``` console
$ kubectl apply --kustomize github.com/intel/pmem-csi/deploy/kustomize/memcached/persistent
service/pmem-memcached created
statefulset.apps/pmem-memcached created
```

We can verify that memcached really does a warm restart by storing
some data, removing the instance and then starting it again.

``` console
$ kubectl wait --for=condition=Ready pods -l app.kubernetes.io/name=pmem-memcached
pod/pmem-memcached-0 condition met
```

Because we use a stateful set, the pod name is deterministic.

There is also a corresponding persistent volume:
``` console
$ kubectl get pvc
NAME                                     STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS       AGE
memcached-data-volume-pmem-memcached-0   Bound    pvc-bb2cde11-6aa2-46da-8521-9bc35c08426d   200Mi      RWO            pmem-csi-sc-ext4   5m45s
```

First set a key using the same approach as before:
``` console
$ kubectl port-forward service/pmem-memcached 11211
Forwarding from 127.0.0.1:11211 -> 11211
Forwarding from [::1]:11211 -> 11211
```

``` console
$ telnet localhost 11211
Trying ::1...
Connected to localhost.
Escape character is '^]'.
```

```
set demo_key 0 500000 10
I am PMEM.
```

```
STORED
```

```
quit
```

Then scale down the number of memcached instances down to zero, then
restart it. To avoid race conditions, it is important to wait for
Kubernetes to catch up:

``` console
$ kubectl scale --replicas=0 statefulset/pmem-memcached
statefulset.apps/pmem-memcached scaled

$ kubectl wait --for delete pod/pmem-memcached-0
Error from server (NotFound): pods "pmem-memcached-0" not found

$ kubectl scale --replicas=1 statefulset/pmem-memcached
statefulset.apps/pmem-memcached scaled

$ kubectl wait --for=condition=Ready pods -l app.kubernetes.io/name=pmem-memcached
pod/pmem-memcached-0 condition met
```

Restart the port forwarding now because it is tied to the previous pod.

Without the persistent volume and the restartable cache, the memcached
cache would be empty now. With `telnet` we can verify that this is not
the case and that the key is still known:
``` console
$ telnet 127.0.0.1 11211
Trying 127.0.0.1...
Connected to 127.0.0.1.
Escape character is '^]'.
```

```
get demo_key
```

```
VALUE demo_key 0 10
I am PMEM.
END
```

```
quit
```

```
Connection closed by foreign host.
```

To clean up, terminate the `kubectl port-forward` command and delete the memcached deployment with:
``` console
$ kubectl delete --kustomize github.com/intel/pmem-csi/deploy/kustomize/memcached/persistent
service "pmem-memcached" deleted
statefulset.apps "pmem-memcached" deleted
```

Beware that at the moment, the volumes need to be removed manually
after removing the stateful set. A [request to automate
that](https://github.com/kubernetes/kubernetes/issues/55045) is open.

``` console
$ kubectl delete pvc -l app.kubernetes.io/name=pmem-memcached
persistentvolumeclaim "memcached-data-volume-pmem-memcached-0" deleted
```

## KubeDB

The [KubeDB operator](https://kubedb.com) >= 0.14.0-beta.1 has
[changes](https://github.com/kubedb/memcached/pull/146) that enable
the usage of PMEM in ephemeral volumes. Management of persistent
volumes and thus the restartable cache feature are not supported.

At this time, the upstream install instructions do not cover
0.14.0-beta.1 yet. Only installation via Helm will be supported and
already works as follows:

```console
$ git clone  https://github.com/kubedb/installer
Cloning into 'installer'...
...

$ cd installer

$ git checkout v0.14.0-beta.1
Note: checking out 'v0.14.0-beta.1'.
...
HEAD is now at a081a36 Prepare for release v0.14.0-beta.1 (#107)

$ helm install kubedb ./charts/kubedb
NAME: kubedb
LAST DEPLOYED: Thu Aug 27 13:02:57 2020
NAMESPACE: default
STATUS: deployed
REVISION: 1
TEST SUITE: None
NOTES:
To verify that KubeDB has started, run:

  kubectl get deployment --namespace default -l "app.kubernetes.io/name=kubedb,app.kubernetes.io/instance=kubedb"

Now install/upgrade appscode/kubedb-catalog chart.

To install, run:

  helm install appscode/kubedb-catalog --name kubedb-catalog --version v0.14.0-beta.1 --namespace default

To upgrade, run:

  helm upgrade kubedb-catalog appscode/kubedb-catalog --version v0.14.0-beta.1 --namespace default

$ helm install kubedb-catalog ./charts/kubedb-catalog
NAME: kubedb-catalog
LAST DEPLOYED: Thu Aug 27 13:03:11 2020
NAMESPACE: default
STATUS: deployed
REVISION: 1
TEST SUITE: None
```

KubeDB is now operational and knows how to install memcached:

``` console
$ kubectl get deployment --namespace default -l "app.kubernetes.io/name=kubedb,app.kubernetes.io/instance=kubedb"
NAME     READY   UP-TO-DATE   AVAILABLE   AGE
kubedb   1/1     1            1           119s

$ kubectl get memcachedversions.catalog.kubedb.com
NAME       VERSION   DB_IMAGE                    DEPRECATED   AGE
1.5        1.5       kubedb/memcached:1.5        true         2m46s
1.5-v1     1.5       kubedb/memcached:1.5-v1     true         2m46s
1.5.22     1.5.22    kubedb/memcached:1.5.22                  2m46s
1.5.4      1.5.4     kubedb/memcached:1.5.4      true         2m46s
1.5.4-v1   1.5.4     kubedb/memcached:1.5.4-v1                2m46s
```

The following command creates a memcached installation with one CSI
ephemeral inline volume. The volume size is 500MiB (specified inside
the custom resource) and the actual file size a bit less at 450MiB
(specified with a [custom
config](https://kubedb.com/docs/v0.13.0-rc.0/guides/memcached/custom-config/using-custom-config/)
to account for filesystem overhead:

``` console
$ kubectl create ns demo
namespace/demo created

$ echo "memory-file = /data/memcached-memory-file" >memcached.conf
$ echo "memory-limit = 450" >>memcached.conf

$ kubectl create configmap -n demo mc-pmem-conf --from-file=memcached.conf
configmap/mc-pmem-conf created
```

``` ShellSession
$ kubectl create -f - <<EOF
apiVersion: kubedb.com/v1alpha1
kind: Memcached
metadata:
  name: memcd-pmem
  namespace: demo
spec:
  replicas: 3
  version: "1.5.22"
  configSource:
    configMap:
      name: mc-pmem-conf
  podTemplate:
    spec:
      resources:
        limits:
          cpu: 500m
          memory: 128Mi
        requests:
          cpu: 250m
          memory: 64Mi
  terminationPolicy: DoNotTerminate
  dataVolume:
    csi:
      driver: "pmem-csi.intel.com"
      volumeAttributes:
        size: 500Mi
EOF
```

The resulting pods then have one additional data volume:

``` console
$ kubectl get -n demo pods
NAME                          READY   STATUS    RESTARTS   AGE
memcd-pmem-56996fb55d-g2z7h   1/1     Running   0          11m
memcd-pmem-56996fb55d-ht2rs   1/1     Running   0          11m
memcd-pmem-56996fb55d-nbrcs   1/1     Running   0          11m

$ kubectl describe -n demo pods/memcd-pmem-56996fb55d-g2z7h
Name:         memcd-pmem-56996fb55d-g2z7h
Namespace:    demo
Priority:     0
Node:         pmem-csi-pmem-govm-worker2/172.17.0.3
Start Time:   Thu, 27 Aug 2020 14:12:09 +0200
Labels:       kubedb.com/kind=Memcached
              kubedb.com/name=memcd-pmem
              pod-template-hash=56996fb55d
Annotations:  <none>
Status:       Running
IP:           10.42.0.3
IPs:
  IP:           10.42.0.3
Controlled By:  ReplicaSet/memcd-pmem-56996fb55d
Init Containers:
  data-volume-owner:
    Container ID:  containerd://9ab4241571ef897733e0b1b368b5f4ada09df6fa155b66b47d6f704ea4895c17
    Image:         kubedb/memcached:1.5.22
    Image ID:      docker.io/kubedb/memcached@sha256:bca5f1901a7304c5eeb17d785341a906cfdf10520fd94f80396097df6bc77e2a
    Port:          <none>
    Host Port:     <none>
    Command:
      /bin/chown
      memcache
      /data
    State:          Terminated
      Reason:       Completed
      Exit Code:    0
      Started:      Thu, 27 Aug 2020 14:18:24 +0200
      Finished:     Thu, 27 Aug 2020 14:18:24 +0200
    Ready:          True
    Restart Count:  0
    Environment:    <none>
    Mounts:
      /data from data-volume (rw)
      /var/run/secrets/kubernetes.io/serviceaccount from memcd-pmem-token-gbcsx (ro)
Containers:
  memcached:
    Container ID:   containerd://df06f7aeb8c14d61c053ccc5891dc2cca8d81544906d3b17301e961fd281ddfa
    Image:          kubedb/memcached:1.5.22
    Image ID:       docker.io/kubedb/memcached@sha256:bca5f1901a7304c5eeb17d785341a906cfdf10520fd94f80396097df6bc77e2a
    Port:           11211/TCP
    Host Port:      0/TCP
    State:          Running
      Started:      Thu, 27 Aug 2020 14:18:24 +0200
    Ready:          True
    Restart Count:  0
    Limits:
      cpu:     500m
      memory:  128Mi
    Requests:
      cpu:        250m
      memory:     64Mi
    Environment:  <none>
    Mounts:
      /data from data-volume (rw)
      /usr/config/ from custom-config (rw)
      /var/run/secrets/kubernetes.io/serviceaccount from memcd-pmem-token-gbcsx (ro)
Conditions:
  Type              Status
  Initialized       True 
  Ready             True 
  ContainersReady   True 
  PodScheduled      True 
Volumes:
  custom-config:
    Type:      ConfigMap (a volume populated by a ConfigMap)
    Name:      mc-pmem-conf
    Optional:  false
  data-volume:
    Type:              CSI (a Container Storage Interface (CSI) volume source)
    Driver:            pmem-csi.intel.com
    FSType:            
    ReadOnly:          false
    VolumeAttributes:      size=500Mi
  memcd-pmem-token-gbcsx:
    Type:        Secret (a volume populated by a Secret)
    SecretName:  memcd-pmem-token-gbcsx
    Optional:    false
QoS Class:       Burstable
Node-Selectors:  <none>
Tolerations:     node.kubernetes.io/not-ready:NoExecute for 300s
                 node.kubernetes.io/unreachable:NoExecute for 300s
Events:
  Type     Reason       Age                  From                                 Message
  ----     ------       ----                 ----                                 -------
  Normal   Scheduled    11m                  default-scheduler                    Successfully assigned demo/memcd-pmem-56996fb55d-g2z7h to pmem-csi-pmem-govm-worker2
  Normal   Pulling      11s                 kubelet, pmem-csi-pmem-govm-worker2  Pulling image "kubedb/memcached:1.5.22"
  Normal   Pulled       11s                 kubelet, pmem-csi-pmem-govm-worker2  Successfully pulled image "kubedb/memcached:1.5.22"
  Normal   Created      11s                 kubelet, pmem-csi-pmem-govm-worker2  Created container data-volume-owner
  Normal   Started      11s                 kubelet, pmem-csi-pmem-govm-worker2  Started container data-volume-owner
  Normal   Pulled       11s                 kubelet, pmem-csi-pmem-govm-worker2  Container image "kubedb/memcached:1.5.22" already present on machine
  Normal   Created      11s                 kubelet, pmem-csi-pmem-govm-worker2  Created container memcached
  Normal   Started      11s                 kubelet, pmem-csi-pmem-govm-worker2  Started container memcached

```

The additional configuration parameters have been passed to memcached:

``` console
$ kubectl exec -n demo memcd-pmem-56996fb55d-g2z7h ps
PID   USER     TIME  COMMAND
    1 memcache  0:00 {start.sh} /bin/sh /usr/local/bin/start.sh
   18 memcache  0:00 memcached --memory-file=/data/memcached-memory-file --memory-limit=450
   28 memcache  0:00 ps
```

To clean up, allow removal of the memcached installation, then delete
the demo namespace and uninstall KubeDB:

``` console
$ kubectl patch -n demo mc/memcd-pmem -p '{"spec":{"terminationPolicy":"WipeOut"}}' --type="merge"
memcached.kubedb.com/memcd-pmem patched
$ kubectl delete ns demo
namespace "demo" deleted
$ helm uninstall kubedb
release "kubedb" uninstalled
$ helm uninstall kubedb-catalog
release "kubedb-catalog" uninstalled
```
