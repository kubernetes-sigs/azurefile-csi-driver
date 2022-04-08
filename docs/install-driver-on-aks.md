## Set up CSI driver on AKS cluster

### Option#1: Enable CSI driver in AKS cluster creation with version < 1.21
> From AKS 1.21, Azure Disk and Azure File CSI drivers would be installed by default.
> 
> Make sure cluster `Control plane` identity(with name `AKS Cluster Name`) has `Contributor` permission on vnet resource group when using NFS protocol or privateEndpoint
> 
Follow AKS doc: [Enable CSI drivers for Azure disks and Azure Files on AKS (preview)](https://docs.microsoft.com/en-us/azure/aks/csi-storage-drivers) 

### Option#2: Enable CSI driver on existing cluster with version < 1.21
> Please remove manual installed open source CSI driver before upgrading to AKS 1.21 next time.
 - Prerequisites

AKS cluster is created with user assigned identity(with naming rule [`AKS Cluster Name-agentpool`](https://docs.microsoft.com/en-us/azure/aks/use-managed-identity#summary-of-managed-identities)) on agent node pool by default, make sure that identity has `Contributor` role on node resource group, follow below instruction to set up `Contributor` permission on node resource group
![image](https://user-images.githubusercontent.com/4178417/120978367-f68f0a00-c7a6-11eb-8e87-89247d1ddc0b.png):


 - Install CSI driver

install latest **released** CSI driver version, following guide [here](./install-azurefile-csi-driver.md)

 - Set up new storage classes
```console
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-csi.yaml
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-nfs.yaml
```
 > follow guide [here](https://github.com/Azure/AKS/issues/118#issuecomment-708257760) to replace built-in storage classes on AKS

 - Remove CSI driver before upgrading to AKS 1.21+

Please remove manual installed open source CSI driver before upgrading to AKS 1.21+, follow the `Clean up CSI driver` section in CSI driver install guide.
