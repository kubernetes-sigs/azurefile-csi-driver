# Azure File CSI Driver for Kubernetes
![linux build status](https://github.com/kubernetes-sigs/azurefile-csi-driver/actions/workflows/linux.yaml/badge.svg)
![windows build status](https://github.com/kubernetes-sigs/azurefile-csi-driver/actions/workflows/windows.yaml/badge.svg)
[![Coverage Status](https://coveralls.io/repos/github/kubernetes-sigs/azurefile-csi-driver/badge.svg?branch=master)](https://coveralls.io/github/kubernetes-sigs/azurefile-csi-driver?branch=master)
[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2Fkubernetes-sigs%2Fazurefile-csi-driver.svg?type=shield)](https://app.fossa.io/projects/git%2Bgithub.com%2Fkubernetes-sigs%2Fazurefile-csi-driver?ref=badge_shield)
[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/azurefile-csi-driver)](https://artifacthub.io/packages/search?repo=azurefile-csi-driver)

### About
This driver allows Kubernetes to access [Azure File](https://docs.microsoft.com/en-us/azure/storage/files/storage-files-introduction) volume using smb and nfs protocols, csi plugin name: `file.csi.azure.com`

Disclaimer: Deploying this driver manually is not an officially supported Microsoft product. For a fully managed and supported experience on Kubernetes, use [AKS with the managed Azure file csi driver](https://learn.microsoft.com/azure/aks/azure-files-csi).

### Project status: GA

### Container Images & Kubernetes Compatibility:
|Driver Version  |Image                                                      | supported k8s version |
|----------------|---------------------------------------------------------- |-----------------------|
|master branch   |mcr.microsoft.com/k8s/csi/azurefile-csi:latest             | 1.21+                 |
|v1.30.2         |mcr.microsoft.com/oss/kubernetes-csi/azurefile-csi:v1.30.2 | 1.21+                 |
|v1.29.5         |mcr.microsoft.com/oss/kubernetes-csi/azurefile-csi:v1.29.5 | 1.21+                 |
|v1.28.10         |mcr.microsoft.com/oss/kubernetes-csi/azurefile-csi:v1.28.10 | 1.21+                 |

### Driver parameters
Please refer to [driver parameters](./docs/driver-parameters.md)

### Prerequisite
#### Option#1: Provide cloud provider config with Azure credentials
 - This option depends on [cloud provider config file](https://github.com/kubernetes/cloud-provider-azure/blob/master/docs/cloud-provider-config.md) (here is [config example](./deploy/example/azure.json)), config file path on different clusters:
   - [AKS](https://docs.microsoft.com/en-us/azure/aks/), [capz](https://github.com/kubernetes-sigs/cluster-api-provider-azure), [aks-engine](https://github.com/Azure/aks-engine): `/etc/kubernetes/azure.json`
   - [Azure RedHat OpenShift](https://docs.openshift.com/container-platform/4.11/storage/container_storage_interface/persistent-storage-csi-azure-file.html): `/etc/kubernetes/cloud.conf`
 - <details> <summary>specify a different config file path via configmap</summary></br>create configmap "azure-cred-file" before driver starts up</br><pre>kubectl create configmap azure-cred-file --from-literal=path="/etc/kubernetes/cloud.conf" --from-literal=path-windows="C:\\k\\cloud.conf" -n kube-system</pre></details>
 - Cloud provider config can also be specified via kubernetes secret, check details [here](./docs/read-from-secret.md)
 - Make sure identity used by driver has `Contributor` role on node resource group and virtual network resource group

#### Option#2: Bring your own storage account (only for SMB protocol)
This option does not depend on cloud provider config file, supports cross subscription and on-premise cluster scenario. Refer to [detailed steps](./deploy/example/e2e_usage.md#option2-bring-your-own-storage-account-only-for-smb-protocol).

### Install driver on a Kubernetes cluster
 - install by [helm charts](./charts)
 - install by [kubectl](./docs/install-azurefile-csi-driver.md)
 - install open source CSI driver on following platforms:
   - [AKS](./docs/install-driver-on-aks.md)
   - [Azure RedHat OpenShift](https://github.com/ezYakaEagle442/aro-pub-storage/blob/master/setup-store-CSI-driver-azure-file.md)
 - install managed CSI driver on following platforms:
   - [AKS](https://learn.microsoft.com/en-us/azure/aks/csi-storage-drivers)
   - [Azure RedHat OpenShift](https://docs.openshift.com/container-platform/4.11/storage/container_storage_interface/persistent-storage-csi-azure-file.html)

### Examples
 - [Basic usage](./deploy/example/e2e_usage.md)
 
### Features
 - [Windows](./deploy/example/windows)
 - [NFS](./deploy/example/nfs)
 - [Volume Snapshot](./deploy/example/snapshot)
 - [Volume Expansion](./deploy/example/resize)
 - [Volume Cloning](./deploy/example/cloning)
 - [Workload identity](./docs/workload-identity-static-pv-mount.md)

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
