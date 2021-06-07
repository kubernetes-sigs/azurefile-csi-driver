## Set up CSI driver on AKS cluster

### Option#1: Enable CSI driver in AKS cluster creation

Follow AKS doc: [Enable CSI drivers for Azure disks and Azure Files on AKS (preview)](https://docs.microsoft.com/en-us/azure/aks/csi-storage-drivers) 

### Option#2: Enable CSI driver on existing cluster
 - Prerequisites

AKS cluster is created with user asissnged identity by default, make sure cluster identity has `Contributor` role on node resource group, follow below instruction to set up `Contributor` role on node resource group
![image](https://user-images.githubusercontent.com/4178417/120978367-f68f0a00-c7a6-11eb-8e87-89247d1ddc0b.png):


 - Install CSI driver

install latest **released** CSI driver version, following guide [here](./install-azurefile-csi-driver.md)

 - Set up new storage classes
```console
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-csi.yaml
```
 > follow guide [here](https://github.com/Azure/AKS/issues/118#issuecomment-708257760) to replace existing storage classes on AKS
