## NFS support

### Feature Status: Alpha
> supported OS: Linux

[NFS volume on Azure Files](https://github.com/RenaShahMSFT/Azure-Files-NFS-Preview) is now available on Azure Canary(centraluseuap) region. This page shows how to use NFS feature on Azure Kubernetes cluster.

#### Prerequisite
 - [Register Azure Storage NFS Preview Program](https://forms.office.com/Pages/ResponsePage.aspx?id=v4j5cvGGr0GRqy180BHbR2Hac0C7FxRCrNVIXjVHNppUQkNVMElIRloyWVlSOUQ5RVMwOFlMNEJUQyQlQCN0PWcu)
 - Register `AllowNfsFileShares`
```console
az feature register --name AllowNfsFileShares --namespace Microsoft.Storage
az feature list -o table --query "[?contains(name, 'Microsoft.Storage/AllowNfsFileShares')].{Name:name,State:properties.state}"
az provider register --namespace Microsoft.Storage
```
 - Create a `Premium_LRS` Azure storage account with following configurations to support NFS share
   - account kind: `FileStorage`
   - enable HTTPS traffic only: `false`
   - select virtual network of agent nodes in `Firewalls and virtual networks`

#### How to use NFS feature
 - Create an Azure File storage class
> specify `storageAccount` and `fsType: nfs` in storage class `parameters`
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
  fsType: nfs
```

run following command to create a storage class:
```console
wget https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-nfs.yaml
# set `storageAccount` in storageclass-azurefile-nfs.yaml
kubectl create -f storageclass-azurefile-nfs.yaml
```

### Example#1
#### Create a deployment with NFS volume
```console
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/statefulset.yaml
```

#### enter the pod to check
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
#### Create a Wordpress application with NFS volume
```console
helm repo add bitnami https://charts.bitnami.com/bitnami
helm install --set persistence.storageClass="azurefile-csi" --set persistence.size=100Gi --generate-name bitnami/wordpress
```
