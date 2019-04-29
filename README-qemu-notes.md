# Notes about VM config for Pmem-CSI development environment

These are some notes about manually created VM configuration for pmem-csi development using emulated NVDIMM as host-backed files.
There exists a newer, more convenient automated method using code in test/ directory, where a four node Kubernetes cluster
can be create by simply typing `make start`.

VM configuration described here was used in early pmem-csi development where a VM was manually created and then used as development host. The initial VM config was created by libvirt/GUI (also doable using virt-install CLI), with some configuration changes made directly in VM-config xml file to emulate a NVDIMM device backed by host file. Two emulated NVDIMMs were tried at some point, but operations on namespaces appear to be more reliable with single emulated NVDIMM.


## maxMemory

```xml
  <maxMemory slots='16' unit='KiB'>67108864</maxMemory>
```

## NUMA config, 2 nodes with 16 GB mem in both

```xml
  <cpu ...>
    <...>
    <numa>
      <cell id='0' cpus='0-3' memory='16777216' unit='KiB'/>
      <cell id='1' cpus='4-7' memory='16777216' unit='KiB'/>
    </numa>
  </cpu>
```

## Emulated 16G NVDIMM with labels support
```xml
    <memory model='nvdimm' access='shared'>
      <source>
        <path>/var/lib/libvirt/images/nvdimm0</path>
      </source>
      <target>
        <size unit='KiB'>16777216</size>
        <node>0</node>
        <label>
          <size unit='KiB'>2048</size>
        </label>
      </target>
      <address type='dimm' slot='0'/>
    </memory>
```

## 16 GB NVDIMM backing file creation example on Host

```sh
dd if=/dev/zero of=/var/lib/libvirt/images/nvdimm0 bs=4K count=4194304
```

Another, quicker way of creating such file is to use `truncate -s` which creates a sparse file:

```sh
truncate -s 16G /var/lib/libvirt/images/nvdimm0
```

qemu using libvirt-created configuration will fill that file with zeroes, as it uses `prealloc=yes` attribute for backing file.

## Labels initialization is needed once per emulated NVDIMM

In the dev.system with emulated NVDIMM, the first OS startup triggers the creation of one device-size pmem region and `ndctl` shows zero remaining available space. To make emulated NVDIMMs usable by `ndctl`, we use labels initialization, which must be performed once after the first bootup with new device(s). You must repeat these steps if the device backing file(s) starts from scratch.

For example, use these commands:

```sh
ndctl disable-region region0
ndctl init-labels nmem0
ndctl enable-region region0

```

## Note about hugepages

Emulated NVDIMM appears not fully working if the VM is configured to use Hugepages. With Hugepages configured, no data survives guest reboots and nothing is ever written into backing store file in host. Configured backing store file is not even part of command line options to qemu. Instead of that, there is some dev/hugepages/libvirt/... path which in reality remains empty in host.
