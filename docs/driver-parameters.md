## Driver Parameters

### Dynamic Provision
  > get a [example](../deploy/example/storageclass-azurefile-csi.yaml)

Name | Meaning | Example | Mandatory | Default value 
--- | --- | --- | --- | ---
skuName | Azure file storage account type (alias: `storageAccountType`) | `Standard_LRS`, `Standard_ZRS`, `Standard_GRS`, `Standard_RAGRS`, `Premium_LRS` | No | `Standard_LRS` <br><br> Note:  <br> 1. minimum file share size of Premium account type is `100GB`<br> 2.[`ZRS` account type](https://docs.microsoft.com/en-us/azure/storage/common/storage-redundancy#zone-redundant-storage) is supported in limited regions <br> 3. Premium files shares is currently only available for LRS
storageAccount | specify Azure storage account name| STORAGE_ACCOUNT_NAME | - No for SMB share </br> - Yes for NFS share|  - For SMB share: if empty, driver will find a suitable storage account that matches `skuName` in the same resource group; if a storage account name is provided, storage account must exist. </br>  - For NFS share, storage account name must be provided
protocol | specify file share protocol | `smb`, `nfs` (`nfs` is in [Preview](https://github.com/kubernetes-sigs/azurefile-csi-driver/tree/master/deploy/example/nfs)) | No | `smb` 
location | specify Azure storage account location | `eastus`, `westus`, etc. | No | if empty, driver will use the same location name as current k8s cluster
resourceGroup | specify the resource group in which Azure file share will be created | existing resource group name | No | if empty, driver will use the same resource group name as current k8s cluster
shareName | specify Azure file share name | existing or new Azure file name | No | if empty, driver will generate an Azure file share name
server | specify Azure storage account server address | existing server address, e.g. `accountname.privatelink.file.core.windows.net` | No | if empty, driver will use default `accountname.file.core.windows.net` or other sovereign cloud account address
storeAccountKey | whether store account key to k8s secret | `true`,`false` | No | `true`
secretNamespace | specify the namespace of secret to store account key | `default`,`kube-system`,etc | No | `default`
tags | [tags](https://docs.microsoft.com/en-us/azure/azure-resource-manager/management/tag-resources) would be created in newly created storage account | tag format: 'foo=aaa,bar=bbb' | No | ""
--- | **Following parameters are only for [VHD disk feature](../deploy/example/disk)** | --- | --- |
fsType | File System Type | `ext4`, `ext3`, `ext2`, `xfs` | Yes | `ext4`
diskName | existing VHD disk file name | `pvc-062196a6-6436-11ea-ab51-9efb888c0afb.vhd` | No |
--- | **Following parameters are only for [NFS feature](../deploy/example/nfs)** | --- | --- |
fsType | File System Type | `nfs` | Yes | `nfs`

### Static Provision(bring your own file share)
  > get a [example](../deploy/example/pv-azurefile-csi.yaml)

Name | Meaning | Available Value | Mandatory | Default value
--- | --- | --- | --- | ---
volumeAttributes.sharename | Azure file share name | existing Azure file share name | Yes |
volumeAttributes.protocol | specify file share protocol | `smb`, `nfs` | No | `smb`
server | specify Azure storage account server address | existing server address, e.g. `accountname.privatelink.file.core.windows.net` | No | if empty, driver will use default `accountname.file.core.windows.net` or other sovereign cloud account address
nodeStageSecretRef.name | secret name that stores storage account name and key | existing secret name |  Yes  |
nodeStageSecretRef.namespace | namespace where the secret is | k8s namespace  |  Yes  |
