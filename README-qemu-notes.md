# Notes about VM config for Pmem-CSI development environment

> **About hugepages:**
> Emulated NVDIMM appears not fully working if the VM is configured to use Hugepages. With Hugepages configured, no data survives guest reboots and nothing is ever written into backing store file in host. Configured backing store file is not even part of command line options to qemu. Instead of that, there is some dev/hugepages/libvirt/... path which in reality remains empty in host.

VM config was originally created by libvirt/GUI (also doable using virt-install CLI), with these configuration changes made directly in VM-config xml file to emulate pair of NVDIMMs backed by host file:

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

## Emulated 2x 8G NVDIMM with labels support

```xml
    <memory model='nvdimm' access='shared'>
      <source>
        <path>/var/lib/libvirt/images/nvdimm0</path>
      </source>
      <target>
        <size unit='KiB'>8388608</size>
        <node>0</node>
        <label>
          <size unit='KiB'>2048</size>
        </label>
      </target>
      <address type='dimm' slot='0'/>
    </memory>
    <memory model='nvdimm' access='shared'>
      <source>
        <path>/var/lib/libvirt/images/nvdimm1</path>
      </source>
      <target>
        <size unit='KiB'>8388608</size>
        <node>1</node>
        <label>
          <size unit='KiB'>2048</size>
        </label>
      </target>
      <address type='dimm' slot='1'/>
    </memory>
```

## 2x 8 GB NVDIMM backing file creation example on Host

```sh
dd if=/dev/zero of=/var/lib/libvirt/images/nvdimm0 bs=4K count=2097152
dd if=/dev/zero of=/var/lib/libvirt/images/nvdimm1 bs=4K count=2097152
```

## Labels initialization is needed once per emulated NVDIMM

In the dev.system with emulated NVDIMM, the first OS startup triggers the creation of one device-size pmem region and `ndctl` shows zero remaining available space. To make emulated NVDIMMs usable by `ndctl`, we use labels initialization, which must be performed once after the first bootup with new device(s). You must repeat these steps if the device backing file(s) starts from scratch.

For example, use these commands for a set of 2 NVDIMMs:

```sh
ndctl disable-region region0
ndctl init-labels nmem0
ndctl enable-region region0

ndctl disable-region region1
ndctl init-labels nmem1
ndctl enable-region region1
```

All `ndctl` sub-commands support a special "all" argument, so you can make changes to all regions, namespaces, and DIMMS with one command as shown below:

```sh
ndctl disable-region all
ndctl init-labels all
ndctl enable-region all
```
