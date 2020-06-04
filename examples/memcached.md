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
