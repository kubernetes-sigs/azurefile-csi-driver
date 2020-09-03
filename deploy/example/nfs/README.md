## NFS support
[NFS volume on Azure Files](https://github.com/RenaShahMSFT/Azure-Files-NFS-Preview) is now in Private Preview. This service is optimized for random access workloads with in-place data updates and provides full POSIX file system support. This page shows how to use NFS feature by Azure File CSI driver on Azure Kubernetes cluster.

#### Feature Status: Alpha
> supported OS: Linux

#### Supported CSI driver version: `v0.8.0`

#### Available regions
`eastus`

#### Prerequisite
 - [Register Azure Storage NFS Preview Program](https://aka.ms/azurefilesnfs/enrollment)
 - Register `AllowNfsFileShares` feature under your subscription
```console
az feature register --name AllowNfsFileShares --namespace Microsoft.Storage
az feature list -o table --query "[?contains(name, 'Microsoft.Storage/AllowNfsFileShares')].{Name:name,State:properties.state}"
az provider register --namespace Microsoft.Storage
```
 - [install CSI driver](https://github.com/kubernetes-sigs/azurefile-csi-driver/blob/master/docs/install-csi-driver-master.md)
 - Create a `Premium_LRS` Azure storage account with following configurations to support NFS share
   - account kind: `FileStorage`
   - secure transfer required(enable HTTPS traffic only): `false`
   - select virtual network of agent nodes in `Firewalls and virtual networks`

#### How to use NFS feature
 - Create an Azure File storage class
> specify `storageAccount` and `protocol: nfs` in storage class `parameters`
> </br>for more details, refer to [driver parameters](../../../docs/driver-parameters.md)
```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: azurefile-csi
provisioner: file.csi.azure.com
parameters:
  resourceGroup: EXISTING_RESOURCE_GROUP_NAME  # optional, only set this when storage account is not in the same resource group as agent node
  storageAccount: EXISTING_STORAGE_ACCOUNT_NAME
  protocol: nfs  # use "fsType: nfs" in v0.8.0
```

run following command to create a storage class:
```console
wget https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-nfs.yaml
# set `storageAccount` in storageclass-azurefile-nfs.yaml
kubectl create -f storageclass-azurefile-nfs.yaml
```

### Example#1
 - Create a deployment with NFS volume
```console
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/statefulset.yaml
```

 - enter pod to check
```console
$ exec -it statefulset-azurefile-0 bash
# df -h
Filesystem      Size  Used Avail Use% Mounted on
...
/dev/sda1                                                                                 29G   11G   19G  37% /etc/hosts
accountname.file.core.windows.net:/accountname/pvc-fa72ec43-ae64-42e4-a8a2-556606f5da38  100G     0  100G   0% /mnt/azurefile
...
```

### Example#2
 - Create a [Wordpress](https://github.com/bitnami/charts/tree/master/bitnami/wordpress) application with NFS volume
```console
helm repo add bitnami https://charts.bitnami.com/bitnami
helm install --set persistence.storageClass="azurefile-csi" --set persistence.size=100Gi --generate-name bitnami/wordpress
```
