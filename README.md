# Azure File CSI Driver for Kubernetes
[![Travis](https://travis-ci.org/kubernetes-sigs/azurefile-csi-driver.svg)](https://travis-ci.org/kubernetes-sigs/azurefile-csi-driver)
[![Coverage Status](https://coveralls.io/repos/github/kubernetes-sigs/azurefile-csi-driver/badge.svg?branch=master)](https://coveralls.io/github/kubernetes-sigs/azurefile-csi-driver?branch=master)
[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2Fkubernetes-sigs%2Fazurefile-csi-driver.svg?type=shield)](https://app.fossa.io/projects/git%2Bgithub.com%2Fkubernetes-sigs%2Fazurefile-csi-driver?ref=badge_shield)

### About
This driver allows Kubernetes to use [Azure File](https://docs.microsoft.com/en-us/azure/storage/files/storage-files-introduction) volume, csi plugin name: `file.csi.azure.com`

### Project status: GA

### Container Images & Kubernetes Compatibility:
|Driver Version  |Image                                           | supported k8s version |
|----------------|----------------------------------------------- |-----------------------|
|master branch   |mcr.microsoft.com/k8s/csi/azurefile-csi:latest  | 1.18+                 |
|v1.3.0          |mcr.microsoft.com/k8s/csi/azurefile-csi:v1.3.0  | 1.18+                 |
|v1.2.0          |mcr.microsoft.com/k8s/csi/azurefile-csi:v1.2.0  | 1.17+                 |
|v1.1.0          |mcr.microsoft.com/k8s/csi/azurefile-csi:v1.1.0  | 1.16+                 |

### Driver parameters
Please refer to [driver parameters](./docs/driver-parameters.md)

### Prerequisite
#### Option#1: Provide cloud provider config with Azure cendentials
 - This option depends on [cloud provider config file](https://github.com/kubernetes/cloud-provider-azure/blob/master/docs/cloud-provider-config.md), usually it's `/etc/kubernetes/azure.json` on all Kubernetes agent nodes deployed by [AKS](https://docs.microsoft.com/en-us/azure/aks/) or [aks-engine](https://github.com/Azure/aks-engine), here is [azure.json example](./deploy/example/azure.json).
 > To specify a different cloud provider config file, create `azure-cred-file` configmap before driver installation, e.g. for OpenShift, it's `/etc/kubernetes/cloud.conf` (make sure config file path is in the `volumeMounts.mountPath`)
 > ```console
 > kubectl create configmap azure-cred-file --from-literal=path="/etc/kubernetes/cloud.conf" --from-literal=path-windows="C:\\k\\cloud.conf" -n kube-system
 > ```
 - This driver also supports [read cloud config from kuberenetes secret](./docs/read-from-secret.md).
 - If cluster identity is [Managed Service Identity(MSI)](https://docs.microsoft.com/en-us/azure/aks/use-managed-identity), make sure user assigned identity has `Contributor` role on node resource group
 - [How to set up CSI driver on Azure RedHat OpenShift(ARO)](https://github.com/ezYakaEagle442/aro-pub-storage/blob/master/setup-store-CSI-driver-azure-file.md)

#### Option#2: Bring your own storage account
This option does not depend on cloud provider config file, supports cross subscription and on-premise cluster scenario. Refer to [detailed steps](./deploy/example/e2e_usage.md#option2-bring-your-own-storage-account).

### Install driver on a Kubernetes cluster
 - install by [kubectl](./docs/install-azurefile-csi-driver.md) (please use helm for RedHat/CentOS)
 - install by [helm charts](./charts) (supports RedHat/CentOS)

### Examples
 - [Basic usage](./deploy/example/e2e_usage.md)
 
### Features
 - [Windows](./deploy/example/windows)
 - [NFS](./deploy/example/nfs)
 - [Snapshot](./deploy/example/snapshot)
 - [Resize](./deploy/example/resize)
 - [On-premise SMB Server mount](./deploy/example/smb-provisioner)
 
### Troubleshooting
 - [CSI driver troubleshooting guide](./docs/csi-debug.md) 

### Support
 - Please see our [support policy][support-policy]

## Kubernetes Development
Please refer to [development guide](./docs/csi-dev.md)

[support-policy]: support.md

### View CI Results
Check testgrid [provider-azure-azurefile-csi-driver](https://testgrid.k8s.io/provider-azure-azurefile-csi-driver) dashboard.

### Links
 - [Kubernetes CSI Documentation](https://kubernetes-csi.github.io/docs)
 - [CSI Drivers](https://github.com/kubernetes-csi/drivers)
 - [Container Storage Interface (CSI) Specification](https://github.com/container-storage-interface/spec)
 - [Use Azure Files with Linux](https://docs.microsoft.com/en-us/azure/storage/files/storage-how-to-use-files-linux)
