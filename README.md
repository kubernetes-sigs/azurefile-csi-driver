# Azure File CSI Driver for Kubernetes

![linux build status](https://github.com/kubernetes-sigs/azurefile-csi-driver/actions/workflows/linux.yaml/badge.svg)
![windows build status](https://github.com/kubernetes-sigs/azurefile-csi-driver/actions/workflows/windows.yaml/badge.svg)
[![Coverage Status](https://coveralls.io/repos/github/kubernetes-sigs/azurefile-csi-driver/badge.svg?branch=master)](https://coveralls.io/github/kubernetes-sigs/azurefile-csi-driver?branch=master)
[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2Fkubernetes-sigs%2Fazurefile-csi-driver.svg?type=shield)](https://app.fossa.io/projects/git%2Bgithub.com%2Fkubernetes-sigs%2Fazurefile-csi-driver?ref=badge_shield)
[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/azurefile-csi-driver)](https://artifacthub.io/packages/search?repo=azurefile-csi-driver)

## About

This driver allows Kubernetes to access [Azure File](https://docs.microsoft.com/en-us/azure/storage/files/storage-files-introduction) volumes using SMB and NFS protocols.

- **CSI plugin name:** `file.csi.azure.com`
- **Project status:** GA

> [!NOTE]
> Deploying this driver manually is not an officially supported Microsoft product. For a fully managed and supported experience on Kubernetes, use [AKS with the managed Azure File CSI driver](https://learn.microsoft.com/azure/aks/azure-files-csi).

## Container Images & Kubernetes Compatibility

| Driver Version | Image                                                            | Supported K8s Version |
|----------------|------------------------------------------------------------------|-----------------------|
| master branch  | `mcr.microsoft.com/k8s/csi/azurefile-csi:latest`                | 1.21+                 |
| v1.35.1        | `mcr.microsoft.com/oss/v2/kubernetes-csi/azurefile-csi:v1.35.1` | 1.21+                 |
| v1.34.4        | `mcr.microsoft.com/oss/v2/kubernetes-csi/azurefile-csi:v1.34.4` | 1.21+                 |
| v1.33.8        | `mcr.microsoft.com/oss/v2/kubernetes-csi/azurefile-csi:v1.33.8` | 1.21+                 |

## Driver Parameters

Please refer to [driver parameters](./docs/driver-parameters.md).

## Prerequisites

### Option 1: Provide cloud provider config with Azure credentials

This option depends on a [cloud provider config file](https://github.com/kubernetes/cloud-provider-azure/blob/master/docs/cloud-provider-config.md) ([config example](./deploy/example/azure.json)). Config file paths on different clusters:

| Platform | Config Path |
|----------|-------------|
| [AKS](https://docs.microsoft.com/en-us/azure/aks/), [CAPZ](https://github.com/kubernetes-sigs/cluster-api-provider-azure), [aks-engine](https://github.com/Azure/aks-engine) | `/etc/kubernetes/azure.json` |
| [Azure Red Hat OpenShift](https://docs.openshift.com/container-platform/4.11/storage/container_storage_interface/persistent-storage-csi-azure-file.html) | `/etc/kubernetes/cloud.conf` |

<details>
<summary>Specify a different config file path via ConfigMap</summary>

Create the ConfigMap `azure-cred-file` before the driver starts up:

```bash
kubectl create configmap azure-cred-file \
  --from-literal=path="/etc/kubernetes/cloud.conf" \
  --from-literal=path-windows="C:\\k\\cloud.conf" \
  -n kube-system
```

</details>

- Cloud provider config can also be specified via a Kubernetes Secret — see [details](./docs/read-from-secret.md).
- Ensure the identity used by the driver has the `Contributor` role on the node resource group and virtual network resource group.

### Option 2: Bring your own storage account (SMB only)

This option does not depend on the cloud provider config file and supports cross-subscription and on-premise cluster scenarios. Refer to [detailed steps](./deploy/example/e2e_usage.md#option2-bring-your-own-storage-account-only-for-smb-protocol).

## Installation

Install the driver on a Kubernetes cluster:

- Install by [Helm charts](./charts)
- Install by [kubectl](./docs/install-azurefile-csi-driver.md)

**Open source CSI driver:**

| Platform | Guide |
|----------|-------|
| AKS | [Install on AKS](./docs/install-driver-on-aks.md) |
| Azure Red Hat OpenShift | [Install on ARO](https://github.com/ezYakaEagle442/aro-pub-storage/blob/master/setup-store-CSI-driver-azure-file.md) |

**Managed CSI driver:**

| Platform | Guide |
|----------|-------|
| AKS | [AKS CSI storage drivers](https://learn.microsoft.com/en-us/azure/aks/csi-storage-drivers) |
| Azure Red Hat OpenShift | [ARO CSI Azure File](https://docs.openshift.com/container-platform/4.11/storage/container_storage_interface/persistent-storage-csi-azure-file.html) |

## Examples

- [Basic usage](./deploy/example/e2e_usage.md)

## Features

- [Windows](./deploy/example/windows)
- [NFS](./deploy/example/nfs)
- [Volume Snapshot and Restore](./deploy/example/snapshot)
- [Volume Expansion](./deploy/example/resize)
- [Volume Cloning](./deploy/example/cloning)
- [Mount with workload identity](./docs/workload-identity-static-pv-mount.md)
- [Mount with managed identity](./docs/managed-identity-mount.md)

## Troubleshooting

- [CSI driver troubleshooting guide](./docs/csi-debug.md)

## Support

Please see our [support policy](support.md).

## Development

Please refer to the [development guide](./docs/csi-dev.md).

## CI Results

Check the TestGrid [provider-azure-azurefile-csi-driver](https://testgrid.k8s.io/provider-azure-azurefile-csi-driver) dashboard.

## Links

- [Kubernetes CSI Documentation](https://kubernetes-csi.github.io/docs)
- [CSI Drivers](https://github.com/kubernetes-csi/drivers)
- [Container Storage Interface (CSI) Specification](https://github.com/container-storage-interface/spec)
- [Use Azure Files with Linux](https://docs.microsoft.com/en-us/azure/storage/files/storage-how-to-use-files-linux)
