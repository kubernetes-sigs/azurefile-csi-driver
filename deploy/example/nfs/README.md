## NFS support
[NFS 4.1 support for Azure Files](https://docs.microsoft.com/en-us/azure/storage/files/files-nfs-protocol) is optimized for random access workloads with in-place data updates and provides full POSIX file system support. This page shows how to use NFS feature by Azure File CSI driver on Azure Kubernetes cluster.
- [Compare access to Azure Files, Blob Storage, and Azure NetApp Files with NFS](https://docs.microsoft.com/en-us/azure/storage/common/nfs-comparison)
- [Encrypt in Transit(EiT) for NFS (Preview)](https://learn.microsoft.com/en-us/azure/storage/files/encryption-in-transit-for-nfs-shares) is now supported from CSI driver v1.33.0, by setting `encryptInTransit: "true"` in the `parameters` of storage class or persistent volume, you can enable data encryption in transit for NFS Azure file volumes. Please ensure that you have registered Encrypt in Transit (EiT) feature before proceeding.
  - Currently, Encrypt in Transit (EiT) feature doesn't support Azure Linux and ARM64 node.
- supported OS: Linux

#### Prerequisite
 - When using AKS managed CSI driver, make sure cluster `Control plane` identity(with name `AKS Cluster Name`) has `Contributor` permission on vnet resource group
 - [Optional] Create a `Premium_LRS` or `Premium_ZRS` Azure storage account with following configurations to support NFS share
   > `Premium_ZRS` account type is only supported in [limited region support](https://docs.microsoft.com/en-us/azure/storage/common/storage-redundancy#zone-redundant-storage)
   - account kind: `FileStorage`
   - Require secure transfer for REST API operations(enable HTTPS traffic only): `false`
   - select virtual network of agent nodes in `Firewalls and virtual networks`
   - specify `storageAccount` in below storage class `parameters`

#### How to use NFS feature
 - Create an Azure File storage class
> specify `protocol: nfs` in storage class `parameters`
> </br>for more details, refer to [driver parameters](../../../docs/driver-parameters.md)
```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: azurefile-csi-nfs
provisioner: file.csi.azure.com
parameters:
  protocol: nfs
  skuName: Premium_LRS  # available values: Premium_LRS, Premium_ZRS
reclaimPolicy: Delete
volumeBindingMode: Immediate
allowVolumeExpansion: true
mountOptions:
  - nconnect=4
  - noresvport
  - actimeo=30
```

run following commands to create a storage class:
```console
wget https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-nfs.yaml
# set `storageAccount` in storageclass-azurefile-nfs.yaml
kubectl create -f storageclass-azurefile-nfs.yaml
```

### Example#1
 - Create a deployment with NFS volume
```console
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/nfs/statefulset.yaml
```

 - enter pod to check
```console
kubectl exec -it statefulset-azurefile-0 -- df -h
```
<pre>
Filesystem      Size  Used Avail Use% Mounted on
...
/dev/sda1                                                                                 29G   11G   19G  37% /etc/hosts
accountname.file.core.windows.net:/accountname/pvc-fa72ec43-ae64-42e4-a8a2-556606f5da38  100G     0  100G   0% /mnt/azurefile
...
</pre>

### Example#2
 - Create a [Wordpress](https://github.com/bitnami/charts/tree/master/bitnami/wordpress) application with NFS volume
```console
helm repo add bitnami https://charts.bitnami.com/bitnami
helm install --set persistence.storageClass="azurefile-csi-nfs" --set persistence.size=100Gi --generate-name bitnami/wordpress
```

#### Links
 - [Azure File NFS mount options on Linux](https://learn.microsoft.com/en-us/azure/storage/files/storage-files-how-to-mount-nfs-shares#mount-options)
 - [Troubleshoot Azure NFS file shares](https://docs.microsoft.com/en-us/azure/storage/files/storage-troubleshooting-files-nfs)
 - [Mount NFS Azure file share on Linux limitations](https://learn.microsoft.com/en-us/azure/storage/files/storage-files-how-to-mount-nfs-shares#limitations)
