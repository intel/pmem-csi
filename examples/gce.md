# Google Cloud Engine

Google Cloud Compute Engine (GCE) offers VMs with Intel® Optane™ DC
persistent memory as part of an alpha program.  Request to get
whitelisted for this program and get information on how to bring up
these VMs by filling out [this
form](https://docs.google.com/forms/d/e/1FAIpQLSeX1tN6Qt-aQUK2iVVioClFX5N-061jqO46vzpHzAPHkjwzVw/viewform).
 
This document explains how to bring up Kubernetes on those machines
with the [scripts from
Kubernetes](https://kubernetes.io/docs/setup/production-environment/turnkey/gce/)
and how to install PMEM-CSI.

It was written for and tested with the v0.5.0 release of PMEM-CSI, but
should also work for other versions.

## Configure and Start

### Testing PMEM without Kubernetes

Of the existing machine images, `debian-9` is known to support
PMEM. When booting up that image on a suitable machine configuration,
a `/dev/pmem0` device is created:

```sh
gcloud alpha compute instances create pmem-debian-9 --machine-type n1-highmem-96-aep  --local-nvdimm size=1600 --zone us-central1-f
```

### Preparing the machine image

But the Kubernetes scripts expect to run with [Container Optimized
OS](https://cloud.google.com/container-optimized-os/docs/). Those
images currently do not support PMEM because the necessary kernel
options are not set.

A custom image must be compiled from source. For that, follow these
[instructions](https://cloud.google.com/container-optimized-os/docs/how-to/building-from-open-source)
and check out the source code with `repo sync`.

Before proceeding, apply the following patch:

```sh
cd src/overlays
patch -p1 <<EOF
commit 148e1095ba56fcf626d184fd2c427bd192e53a28
Author: Patrick Ohly <patrick.ohly@intel.com>
Date:   Fri Aug 2 10:46:30 2019 +0200

    lakitu: kernel: enable PMEM support
    
    Some alpha machine types in GCE support persistent memory (aka
    nvdimm). These kernel changes are necessary to use that hardware
    and/or experiment with PMEM on normal machine types.
    
    Change-Id: I1682e6293f492128994ed7635bcc44368009db59

diff --git a/overlay-lakitu/sys-kernel/lakitu-kernel-4_19/files/base.config b/overlay-lakitu/sys-kernel/lakitu-kernel-4_19/files/base.config
index 117772d9ad..a48263de3d 100644
--- a/overlay-lakitu/sys-kernel/lakitu-kernel-4_19/files/base.config
+++ b/overlay-lakitu/sys-kernel/lakitu-kernel-4_19/files/base.config
@@ -357,7 +357,6 @@ CONFIG_ARCH_SPARSEMEM_DEFAULT=y
 CONFIG_ARCH_SELECT_MEMORY_MODEL=y
 CONFIG_ARCH_PROC_KCORE_TEXT=y
 CONFIG_ILLEGAL_POINTER_VALUE=0xdead000000000000
-# CONFIG_X86_PMEM_LEGACY is not set
 CONFIG_X86_CHECK_BIOS_CORRUPTION=y
 CONFIG_X86_BOOTPARAM_MEMORY_CORRUPTION_CHECK=y
 CONFIG_X86_RESERVE_LOW=64
@@ -467,7 +466,6 @@ CONFIG_ACPI_HOTPLUG_IOAPIC=y
 # CONFIG_ACPI_CUSTOM_METHOD is not set
 # CONFIG_ACPI_BGRT is not set
 # CONFIG_ACPI_REDUCED_HARDWARE_ONLY is not set
-# CONFIG_ACPI_NFIT is not set
 CONFIG_HAVE_ACPI_APEI=y
 CONFIG_HAVE_ACPI_APEI_NMI=y
 # CONFIG_ACPI_APEI is not set
@@ -842,7 +840,6 @@ CONFIG_SPARSEMEM_VMEMMAP=y
 CONFIG_HAVE_MEMBLOCK=y
 CONFIG_HAVE_MEMBLOCK_NODE_MAP=y
 CONFIG_ARCH_DISCARD_MEMBLOCK=y
-# CONFIG_MEMORY_HOTPLUG is not set
 CONFIG_SPLIT_PTLOCK_CPUS=4
 CONFIG_COMPACTION=y
 CONFIG_MIGRATION=y
@@ -2532,11 +2529,29 @@ CONFIG_RAS=y
 # Android
 #
 # CONFIG_ANDROID is not set
-# CONFIG_LIBNVDIMM is not set
-CONFIG_DAX=y
-# CONFIG_DEV_DAX is not set
 CONFIG_NVMEM=y
 
+# Base on https://nvdimm.wiki.kernel.org/?#quick_setup_guide
+# Only options not already set as intended elsewhere are here.
+CONFIG_ZONE_DEVICE=y
+CONFIG_MEMORY_HOTPLUG=y
+CONFIG_MEMORY_HOTREMOVE=y
+CONFIG_ACPI_NFIT=m
+CONFIG_X86_PMEM_LEGACY=m
+CONFIG_OF_PMEM=m
+CONFIG_LIBNVDIMM=m
+CONFIG_BLK_DEV_PMEM=m
+CONFIG_BTT=y
+CONFIG_NVDIMM_PFN=y
+CONFIG_NVDIMM_DAX=y
+CONFIG_DAX=y
+CONFIG_DEV_DAX=m
+CONFIG_DEV_DAX_PMEM=m
+CONFIG_DEV_DAX_KMEM=m
+
+# Needed for "mount -o dax".
+CONFIG_FS_DAX=y
+
 #
 # HW tracing support
 #
@@ -2573,7 +2588,6 @@ CONFIG_FS_MBCACHE=y
 # CONFIG_BTRFS_FS is not set
 # CONFIG_NILFS2_FS is not set
 # CONFIG_F2FS_FS is not set
-# CONFIG_FS_DAX is not set
 CONFIG_FS_POSIX_ACL=y
 CONFIG_EXPORTFS=y
 # CONFIG_EXPORTFS_BLOCK_OPS is not set
diff --git a/overlay-lakitu/sys-kernel/lakitu-kernel-4_19/lakitu-kernel-4_19-4.19.58-r305.ebuild b/overlay-lakitu/sys-kernel/lakitu-kernel-4_19/lakitu-kernel-4_19-4.19.58-r305.ebuild
index 5337c40fc5..781ac11d94 100644
--- a/overlay-lakitu/sys-kernel/lakitu-kernel-4_19/lakitu-kernel-4_19-4.19.58-r305.ebuild
+++ b/overlay-lakitu/sys-kernel/lakitu-kernel-4_19/lakitu-kernel-4_19-4.19.58-r305.ebuild
@@ -92,4 +92,4 @@ src_install() {
 # NOTE: There's nothing magic keeping this number prime but you just need to
 # make _any_ change to this file.  ...so why not keep it prime?
 #
-# The coolest prime number is: 7
+# The coolest prime number is: 11
EOF
```

This patch is also available [on
GitHub](https://github.com/pohly/chromiumos-overlays-board-overlays/commits/lakita-4-19-pmem).

Now [build packages and a "test" image](https://cloud.google.com/container-optimized-os/docs/how-to/building-from-open-source#building_a_image).

That test image allows root access with "test0000" as password, which
can be used to verify that PMEM support is active by booting the image
under QEMU:

```sh
qemu-system-x86_64 -enable-kvm -machine accel=kvm,usb=off  -nographic \
                   -net nic,model=virtio -net user,hostfwd=tcp:127.0.0.1:9223-:22 \
                   -hda ./src/build/images/lakitu/latest/chromiumos_test_image.bin \
                   -m 2G,slots=2,maxmem=34G -smp 4 -machine pc,accel=kvm,nvdimm=on \
                   -object memory-backend-file,id=mem1,share=on,mem-path=/tmp/nvdimm1,size=32768M \
                   -device nvdimm,id=nvdimm1,memdev=mem1,label-size=2097152
```

As for the `debian-9` image on a GCE VM, this machine will have a `/dev/pmem0`.

Now build a normal image ("base" or "dev") and [create a GCE image
from it](https://cloud.google.com/container-optimized-os/docs/how-to/building-from-open-source#running_on_compute_engine).

### Starting and stopping Kubernetes

A modified version of the Kubernetes scripts are required. Get those with:

```sh
git clone --branch gce-node-nvdimm https://github.com/pohly/kubernetes.git
```

These scripts are normally packaged as part of a release. To call them
from that branch, one has to download one additional file:

```sh
cd kubernetes
mkdir server
curl -L https://dl.k8s.io/v1.15.1/kubernetes.tar.gz | tar -zxf - -O kubernetes/server/kubernetes-manifests.tar.gz  >server/kubernetes-manifests.tar.gz
```

Then create a cluster with one master and three worker nodes:

```sh
NUM_NODES=3 \
KUBE_GCE_ZONE=us-central1-f \
KUBE_GCE_NODE_IMAGE=<your image name from the previous step> \
KUBE_GCE_NODE_PROJECT=<your project ID> \
PROJECT=<your project ID> \
NODE_NVDIMM=size=1600 \
NODE_SIZE=n1-highmem-96-aep \
KUBE_VERSION=v1.15.1 \
   cluster/kube-up.sh
```

Accept when the script asks to download artifacts.

The advantage of this script over a manual approach (like manually
creating machine instances and then calling `kubeadm`) is that it also
handles firewall, logging and etcd configuration. For additional
parameters, see
https://github.com/pohly/kubernetes/blob/gce-node-nvdimm/cluster/gce/config-default.sh

To stop the cluster, use the same env variables for the
`cluster/kube-down.sh` script.

### Installing PMEM-CSI

After the previous step, `kubectl` works and is configured to use the
new cluster. What follows next are the steps explained in more details
in the top-level README's [Run PMEM-CSI on
Kubernetes](../run-pmem-csi-on-kubernetes) section.

First the worker nodes need to be labeled:

```sh
NUM_NODES=3
for i in $( seq 0 $(($NUM_NODES - 1)) ); do kubectl label node kubernetes-minion-node-$i storage=pmem; done
```

Then certificates need to be created. This currently works best with
scripts from the pmem-csi repo:

```sh
git clone --branch release-0.5 https://github.com/intel/pmem-csi
cd pmem-csi
curl -L https://pkg.cfssl.org/R1.2/cfssl_linux-amd64 -o _work/bin/cfssl --create-dirs
curl -L https://pkg.cfssl.org/R1.2/cfssljson_linux-amd64 -o _work/bin/cfssljson --create-dirs
chmod a+x _work/bin/cfssl _work/bin/cfssljson
PATH="$PATH:$PWD/_work/bin" ./test/setup-ca-kubernetes.sh
```

As in QEMU, the GCE VMs come up with the entire PMEM already set up
for usage as `/dev/pmem0`. PMEM-CSI v0.5.0 will not use space that
looks like it is reserved for some other purpose already. However,
under GCE the commands for making that space available for
PMEM-CSI (`ndctl disable-region region0`, `ndctl init-labels nmem0`,
`ndctl enable-region region0`) do not work because the PMEM that
is available under GCE does not support labels.

That rules out running PMEM-CSI in direct mode where it creates
namespaces for volumes. But LVM mode works, we just need to create a
suitable volume group manually on top of the existing
`/dev/pmem0`. Because COS does not contain the LVM commands, we do
that in the PMEM-CSI image:

```sh
NUM_NODES=3
for i in $( seq 0 $(($NUM_NODES - 1)) ); do
    gcloud compute ssh --zone us-central1-f kubernetes-minion-node-$i -- docker run --privileged --rm -i --entrypoint /bin/sh intel/pmem-csi-driver:v0.5.0 <<EOF
vgcreate --force ndbus0region0fsdax /dev/pmem0 && vgchange --addtag fsdax ndbus0region0fsdax
EOF
done
```

With the nodes set up like that, we can proceed to deploy PMEM-CSI:

```sh
kubectl create -f https://raw.githubusercontent.com/intel/pmem-csi/v0.5.0/deploy/kubernetes-1.14/pmem-csi-lvm.yaml
kubectl create -f https://raw.githubusercontent.com/intel/pmem-csi/v0.5.0/deploy/common/pmem-storageclass-ext4.yaml
kubectl create -f https://raw.githubusercontent.com/intel/pmem-csi/v0.5.0/deploy/common/pmem-storageclass-xfs.yaml
```

### Testing PMEM-CSI

This brings up the example apps, one using `ext4`, the other `xfs`:
```sh
kubectl create -f https://raw.githubusercontent.com/intel/pmem-csi/v0.5.0/deploy/common/pmem-pvc.yaml
kubectl create -f https://raw.githubusercontent.com/intel/pmem-csi/v0.5.0/deploy/common/pmem-app.yaml
```

It is expected that `my-csi-app-2` will never start because the COS
kernel lacks support for xfs. But `my-csi-app-1` comes up:

```sh
$ kubectl get pv
NAME                                       CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM                       STORAGECLASS       REASON   AGE
pvc-0c2ebc68-cd77-4c08-9fbb-f8a5d33440b9   4Gi        RWO            Delete           Bound    default/pmem-csi-pvc-ext4   pmem-csi-sc-ext4            2m52s
pvc-c04c27a9-4b9e-4a28-9374-e9e6c5cbf18c   4Gi        RWO            Delete           Bound    default/pmem-csi-pvc-xfs    pmem-csi-sc-xfs             2m52s

$ kubectl get pods -o wide
NAME                    READY   STATUS              RESTARTS   AGE   IP            NODE                       NOMINATED NODE   READINESS GATES
my-csi-app-1            1/1     Running             0          13s   10.64.2.3     kubernetes-minion-node-1   <none>           <none>
my-csi-app-2            0/1     ContainerCreating   0          13s   <none>        kubernetes-minion-node-1   <none>           <none>
pmem-csi-controller-0   2/2     Running             0          33s   10.64.1.12    kubernetes-minion-node-2   <none>           <none>
pmem-csi-node-9gfzv     2/2     Running             0          33s   10.128.0.40   kubernetes-minion-node-2   <none>           <none>
pmem-csi-node-9ltng     2/2     Running             0          33s   10.128.0.41   kubernetes-minion-node-0   <none>           <none>
pmem-csi-node-ftd8c     2/2     Running             0          33s   10.128.0.39   kubernetes-minion-node-1   <none>           <none>
```


## Open issues

### Extend kernel config for COS

The patch from
https://github.com/pohly/chromiumos-overlays-board-overlays/commits/lakita-4-19-pmem
needs to be merged by Google. Then building a custom COS image is no
longer necessary.

### Support alpha instance-templates in gcloud

It is possible to define an instance template that uses the alpha machines:

```sh
gcloud alpha compute instance-templates create kubernetes-minion-template --machine-type n1-highmem-96-aep --local-nvdimm size=1600 --region us-central1
```

But then using that template fails (regardless whether `alpha` is used or not):

```sh
$ gcloud alpha compute instance-groups managed create kubernetes-minion-group --zone us-central1-b --base-instance-name kubernetes-minion-group --size 3 --template kubernetes-minion-template
ERROR: (gcloud.alpha.compute.instance-groups.managed.create) Could not fetch resource:
 - Internal error. Please try again or contact Google Support. (Code: '58F5F51C192A0.A2E8610.8D038E81')
```

If this was supported, the Kubernetes script changes would become
cleaner and may have a chance of getting merged upstream.

### Merge support for PMEM into Kubernetes scripts

No PR has been created for
https://github.com/pohly/kubernetes/commits/gce-node-nvdimm at this
time. First gcloud should be enhanced and these instructions should be
made public.

### Avoid the need to check out PMEM-CSI source code

PMEM-CSI can be deployed almost without using its source code. The
only exception is the creation of certificates.
https://github.com/intel/pmem-csi/issues/321 is open regarding that.

### Avoid manual LVM and ndctl commands

`pmem-vgm` already contains code for this, it just doesn't work on GCE
because it ignores the existing `/dev/pmem0`. We should simplify the
work of the admin and support configuring in Kubernetes how PMEM is to
be used, then have PMEM-CSI do the necessary initialization. This
probably fits into the upcoming [installation via
operator](https://github.com/intel/pmem-csi/issues/376).

### Debian 10 and others

Someone needs to investigate whether Debian 10 works with PMEM under
QEMU (may or may not work) and change the GCE image so that it also
works under GCE.

The same would be useful for other images that users of GCE may want
to use with PMEM.

## Debugging and development

### Running ndctl

The COS image does not have `ndctl`. The following `DaemonSet` instead
runs commands inside the `pmem-csi-driver` image. This is an
alternative to running inside Docker.

```sh
kubectl create -f - <<EOF
apiVersion: apps/v1beta2
kind: DaemonSet
metadata:
  name: pmem-csi-init
spec:
  selector:
    matchLabels:
      app: pmem-csi-node
  template:
    metadata:
      labels:
        app: pmem-csi-node
    spec:
      containers:
      - command:
        - /bin/sh
        - -c
        - set -x; ls -l /dev/pmem*; ndctl list -RN && ndctl disable-region region0 && ndctl init-labels nmem0 && ndctl enable-region region0 && ndctl list -RN && (ls -l /dev/pmem* || true) && sleep 10000
        image: intel/pmem-csi-driver:v0.5.0-rc4
        name: ndctl
        securityContext:
          privileged: true
        volumeMounts:
        - mountPath: /dev
          name: dev-dir
        - mountPath: /sys
          name: sys-dir
      nodeSelector:
        storage: pmem
      volumes:
      - hostPath:
          path: /dev
          type: DirectoryOrCreate
        name: dev-dir
      - hostPath:
          path: /sys
          type: DirectoryOrCreate
        name: sys-dir
EOF
```

The expected result is that the pods keep running, so the output can be checked:

```sh
$ kubectl get pods -o wide
NAME                  READY   STATUS    RESTARTS   AGE   IP           NODE                       NOMINATED NODE   READINESS GATES
pmem-csi-init-5dswq   1/1     Running   0          12m   10.64.1.16   kubernetes-minion-node-0   <none>           <none>
pmem-csi-init-gb5k5   1/1     Running   0          12m   10.64.2.5    kubernetes-minion-node-2   <none>           <none>
pmem-csi-init-tqpjl   1/1     Running   0          12m   10.64.3.7    kubernetes-minion-node-1   <none>           <none>

$ kubectl logs pmem-csi-init-5dswq
+ ls -l /dev/pmem0
brw-rw---- 1 root disk 259, 0 Aug  2 18:49 /dev/pmem0
+ ndctl list -RN
{
  "regions":[
    {
      "dev":"region0",
      "size":1717986918400,
      "available_size":0,
      "max_available_extent":0,
      "type":"pmem",
      "persistence_domain":"unknown",
      "namespaces":[
        {
          "dev":"namespace0.0",
          "mode":"fsdax",
          "map":"mem",
          "size":1717986918400,
          "sector_size":512,
          "blockdev":"pmem0"
        }
      ]
    }
  ]
}
+ ndctl disable-region region0
disabled 1 region
+ ndctl init-labels nmem0
initialized 0 nmem
+ ndctl enable-region region0
enabled 1 region
+ ndctl list -RN
{
  "regions":[
    {
      "dev":"region0",
      "size":1717986918400,
      "available_size":0,
      "max_available_extent":0,
      "type":"pmem",
      "persistence_domain":"unknown",
      "namespaces":[
        {
          "dev":"namespace0.0",
          "mode":"fsdax",
          "map":"mem",
          "size":1717986918400,
          "sector_size":512,
          "blockdev":"pmem0"
        }
      ]
    }
  ]
}
+ ls -l /dev/pmem0
brw-rw---- 1 root disk 259, 0 Aug  2 18:53 /dev/pmem0
+ sleep 10000
```

The output above shows that `ndctl init-labels` doesn't do anything.
