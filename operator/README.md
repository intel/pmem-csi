<!-- TOC -->
- [PMEM-CSI Operator](#pmem-csi-for-kubernetes)
  - [About](#about)
  - [Usage](#usage)
    - [Pre-requisites](#pre-requisites)
    - [Deploy PMEM-CSI operator](#deploy-pmem-csi-operator)
    - [Deployment CRD API](#deployment-crd-api)
  - [Example Deployment](#example-deployment)

<!-- /TOC -->
# PMEM-CSI-Operator

## About

Kubernetes operator for [PMEM-CSI](https://github.com/intel/pmem-csi) driver deployment. Using this operator one can easily deploy and manage PMEM-CSI driver in a Kubernetes cluster.

The PMEM-CSI operator is based on the CoreOS [operator-sdk](https://github.com/operator-framework/operator-sdk) tools and APIs.

## Usage

### Pre-requisites

A running Kubernetes cluster with one or more nodes installed with PMEM hardware(NVDIMM). The persistent memory on these nodes must be [pre-provisioned](../README.md#persistent-memory-pre-provisioning).

### Deploy PMEM-CSI operator

<!-- The assumptions:
  a) pmem-csi-operator binary is part of released intel/pmem-csi-driver image and no additional steps required to build the images
-->

```sh
$ kubectl create -f https://raw.githubusercontent.com/intel/pmem-csi/operator/operator/deploy/operator.yaml
```

### Deployment CRD API

Current PMEM-CSI Deployment object supports below API:

#### Deployment

|Field | type | Description |
|---|---|---|
| apiVersion | string  | `pmem-csi.intel.com/v1alpha1`|
| kind | string | `Deployment`|
| metadata | [ObjectMeta](https://git.k8s.io/community/contributors/devel/api-conventions.md#spec-and-status) | Standard objects metadata |
| spec | [DeploymentSpec](#deployment-spec) | Specification of the desired behavior of the deployment |

#### DeploymentSpec

|Field | type | Description | Default Value |
|---|---|---|---|
| driverName | string | Unique CSI driver name to use | `pmem-csi.intel.com` |
| image | string | PMEM-CSI docker image name used for the deployment | `intel/pmem-csi-driver:canary` |
| provisionerImage | string | [CSI provisioner](https://github.com/kubernetes-csi/external-provisioner) docker image name | `quay.io/k8scsi/csi-provisioner:v1.2.1` |
| registrarImage | string | [CSI node driver registrar](https://github.com/kubernetes-csi/node-driver-registrar) docker image name | `quay.io/k8scsi/csi-node-driver-registrar:v1.1.0` |
| pullPolicy | string | Docker image pull policy. either one of `Always`, `Never`, `IfNotPresent` | `IfNotPresent` |
| logLevel | integer | PMEM-CSI driver logging level | 3 |
| deviceMode | string | Device management mode to use. Supports one of `lvm` or `direct` | `lvm`
| controllerResources | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.12/#resourcerequirements-v1-core) | Describes the compute resource requirements for controller pod |
| nodeResources | [ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.12/#resourcerequirements-v1-core) | Describes the compute resource requirements for the pods running on node(s) |

## Example Deployment

Once the [operator deployed](#deploy-PMEM-CSI-operator) is in `Running` state, it is ready to handle PMEM-CSI `Deployment` objects in `pmem-csi.intel.com` API group.

Here is an example driver deployment with custom driver image and pod resources:

```sh
$ kubectl create -f - <<EOF
apiVersion: pmem-csi.intel.com/v1alpha1
kind: Deployment
metadata:
  name: pmem-deployment
spec:
  image: localhost/pmem-csi-driver:canary
  deviceMode: lvm
  controllerResources:
    request:
      cpu: "200m"
      memory: "100Mi"
  nodeResources:
    request:
      cpu: "200m"
      memory: "100Mi"
EOF
```

Once the above deployment installation is successful, we can see all the driver pods in `Running` state:
```sh
$ kubectl get deployments.pmem-csi.intel.com
NAME                 AGE
pmem-deployment      50s

$ kubectl get po
NAME                    READY   STATUS    RESTARTS   AGE
pmem-csi-controller-0   2/2     Running   0          51s
pmem-csi-node-4x7cv     2/2     Running   0          50s
pmem-csi-node-6grt6     2/2     Running   0          50s
pmem-csi-node-msgds     2/2     Running   0          51s
```

> **WARNING**: If one wants to run multiple driver deployments, make sure that those deployments:
>  - do not run more than one driver on the same node
>  - driver names\(`driverName`\) are unique.
