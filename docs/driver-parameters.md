## Driver Parameters
`file.csi.azure.com` driver parameters

### Dynamic Provision
  > get a [example](../deploy/example/storageclass-azurefile-csi.yaml)

Name | Meaning | Example | Mandatory | Default value 
--- | --- | --- | --- | ---
skuName | Azure file storage account type (alias: `storageAccountType`) | `Standard_LRS`, `Standard_ZRS`, `Standard_GRS`, `Standard_RAGRS`, `Standard_RAGZRS`, `Premium_LRS`, `Premium_ZRS` | No | `Standard_LRS` <br><br> Note:  <br> 1. minimum file share size of Premium account type is `100GB`<br> 2.[`ZRS` account type](https://docs.microsoft.com/en-us/azure/storage/common/storage-redundancy#zone-redundant-storage) is supported in limited regions <br> 3. NFS file share only supports Premium account type
storageAccount | specify Azure storage account name| STORAGE_ACCOUNT_NAME | No | if empty, driver will find a suitable storage account that matches account settings in the same resource group; if a storage account name is provided, storage account must exist.
enableLargeFileShares | specify whether to use a storage account with large file shares enabled or not. If this flag is set to true and a storage account with large file shares enabled doesn't exist, a new storage account with large file shares enabled will be created. This flag should be used with the standard sku as the storage accounts created with premium sku have largeFileShares option enabled by default.  | `true`,`false` | No | `false`
protocol | file share protocol | `smb`, `nfs` | No | `smb`
networkEndpointType | specify network endpoint type for the storage account created by driver. If `privateEndpoint` is specified, a private endpoint will be created for the storage account. For other cases, a service endpoint will be created by default. | "",`privateEndpoint` | No | ``
location | specify Azure storage account location | `eastus`, `westus`, etc. | No | if empty, driver will use the same location name as current k8s cluster
resourceGroup | specify the resource group in which Azure file share will be created | existing resource group name | No | if empty, driver will use the same resource group name as current k8s cluster
shareName | specify Azure file share name | existing or new Azure file name | No | if empty, driver will generate an Azure file share name
shareNamePrefix | specify Azure file share name prefix created by driver | can only contain lowercase letters, numbers, hyphens, and length should be less than 21 | No |
folderName | specify folder name in Azure file share | existing folder name in Azure file share | No | if folder name does not exist in file share, mount would fail
shareAccessTier | [Access tier for file share](https://docs.microsoft.com/en-us/azure/storage/files/storage-files-planning#storage-tiers) | GpV2 account can choose between `TransactionOptimized` (default), `Hot`, and `Cool`. FileStorage account can choose `Premium` | No | empty(use default setting for different storage account types)
accountAccessTier | [Access tier for storage account](https://learn.microsoft.com/en-us/azure/storage/blobs/access-tiers-overview) | Standard account can choose `Hot` or `Cool`, and Premium account can only choose `Premium` | No | empty(use default setting for different storage account types)
server | specify Azure storage account server address | existing server address, e.g. `accountname.privatelink.file.core.windows.net` | No | if empty, driver will use default `accountname.file.core.windows.net` or other sovereign cloud account address
disableDeleteRetentionPolicy | specify whether disable DeleteRetentionPolicy for storage account created by driver | `true`,`false` | No | `false`
allowBlobPublicAccess | Allow or disallow public access to all blobs or containers for storage account created by driver | `true`,`false` | No | `false`
requireInfraEncryption | specify whether or not the service applies a secondary layer of encryption with platform managed keys for data at rest for storage account created by driver | `true`,`false` | No | `false`
storageEndpointSuffix | specify Azure storage endpoint suffix | `core.windows.net`, `core.chinacloudapi.cn`, etc | No | if empty, driver will use default storage endpoint suffix according to cloud environment, e.g. `core.windows.net`
tags | [tags](https://docs.microsoft.com/en-us/azure/azure-resource-manager/management/tag-resources) would be created in newly created storage account | tag format: 'foo=aaa,bar=bbb' | No | ""
matchTags | whether matching tags when driver tries to find a suitable storage account | `true`,`false` | No | `false`
--- | **Following parameters are only for SMB protocol** | --- | --- |
subscriptionID | specify Azure subscription ID in which Azure file share will be created | Azure subscription ID | No | if not empty, `resourceGroup` must be provided
storeAccountKey | whether store account key to k8s secret <br><br> Note:  <br> `false` means driver would leverage kubelet identity to get account key | `true`,`false` | No | `true`
secretName | specify secret name to store account key | | No |
secretNamespace | specify the namespace of secret to store account key | `default`,`kube-system`, etc | No | pvc namespace (`csi.storage.k8s.io/pvc/namespace`)
useDataPlaneAPI | specify whether use [data plane API](https://github.com/Azure/azure-sdk-for-go/blob/master/storage/share.go) for file share create/delete/resize, this could solve the SRP API throltting issue since data plane API has almost no limit, while it would fail when there is firewall or vnet setting on storage account | `true`,`false` | No | `false`
--- | **Following parameters are only for NFS protocol** | --- | --- |
rootSquashType | specify root squashing behavior on the share. The default is `NoRootSquash` | `AllSquash`, `NoRootSquash`, `RootSquash` | No |
mountPermissions | mounted folder permissions. The default is `0777`, if set as `0`, driver will not perform `chmod` after mount | `0777` | No |
--- | **Following parameters are only for vnet setting, e.g. NFS, private end point** | --- | --- |
vnetResourceGroup | specify vnet resource group where virtual network is | existing resource group name | No | if empty, driver will use the `vnetResourceGroup` value in azure cloud config file
vnetName | virtual network name | existing virtual network name | No | if empty, driver will use the `vnetName` value in azure cloud config file
subnetName | subnet name | existing subnet name of the agent node | No | if empty, driver will use the `subnetName` value in azure cloud config file
fsGroupChangePolicy | indicates how volume's ownership will be changed by the driver, pod `securityContext.fsGroupChangePolicy` is ignored  | `OnRootMismatch`(by default), `Always`, `None` | No | `OnRootMismatch`
--- | **Following parameters are only for experimental [VHD disk feature](../deploy/example/disk)** | --- | --- |
fsType | File System Type | `ext4`, `ext3`, `ext2`, `xfs` | Yes | `ext4`
diskName | existing VHD disk file name | `pvc-062196a6-6436-11ea-ab51-9efb888c0afb.vhd` | No |

 - account tags format created by dynamic provisioning
```
k8s-azure-created-by: azure
```

 - VolumeID(`volumeHandle`) is the identifier of the volume handled by the driver, format of VolumeID: 
```
{resource-group-name}#{account-name}#{file-share-name}#{placeholder}#{uuid}#{secret-namespace}
```
 > `placeholder`, `uuid`, `secret-namespace` are optional

 - file share name format created by dynamic provisioning(example)
```
pvc-92a4d7f2-f23b-4904-bad4-2cbfcff6e388
```

### Static Provision(bring your own file share)
  > get a [smb pv example](../deploy/example/pv-azurefile-csi.yaml)

  > get a [nfs pv example](../deploy/example/pv-azurefile-nfs.yaml)

Name | Meaning | Available Value | Mandatory | Default value
--- | --- | --- | --- | ---
volumeHandle | Specify a value the driver can use to uniquely identify the share in the cluster. | A recommended way to produce a unique value is to combine the globally unique storage account name and share name: {account-name}_{file-share-name}. Note: The # character is reserved for internal use. | Yes |
volumeAttributes.resourceGroup | Azure resource group name | existing resource group name | No | if empty, driver will use the same resource group name as current k8s cluster
volumeAttributes.storageAccount | existing storage account name | existing storage account name | Yes |
volumeAttributes.shareName | Azure file share name | existing Azure file share name | Yes |
volumeAttributes.folderName | specify folder name in Azure file share | existing folder name in Azure file share | No | if folder name does not exist in file share, mount would fail
volumeAttributes.protocol | specify file share protocol | `smb`, `nfs` | No | `smb`
volumeAttributes.server | specify Azure storage account server address | existing server address, e.g. `accountname.privatelink.file.core.windows.net` | No | if empty, driver will use default `accountname.file.core.windows.net` or other sovereign cloud account address
--- | **Following parameters are only for SMB protocol** | --- | --- |
volumeAttributes.secretName | secret name that stores storage account name and key | | No |
volumeAttributes.secretNamespace | secret namespace | `default`,`kube-system`, etc | No | pvc namespace (`csi.storage.k8s.io/pvc/namespace`)
nodeStageSecretRef.name | secret name that stores storage account name and key | existing secret name |  Yes  |
nodeStageSecretRef.namespace | secret namespace | k8s namespace  |  Yes  |
--- | **Following parameters are only for NFS protocol** | --- | --- |
volumeAttributes.fsGroupChangePolicy | indicates how volume's ownership will be changed by the driver, pod `securityContext.fsGroupChangePolicy` is ignored  | `OnRootMismatch`(by default), `Always`, `None` | No | `OnRootMismatch`
volumeAttributes.mountPermissions | mounted folder permissions. The default is `0777` |  | No |

 - create a Kubernetes secret for `nodeStageSecretRef.name`
 ```console
kubectl create secret generic azure-storage-account-{accountname}-secret --from-literal=azurestorageaccountname="xxx" --from-literal azurestorageaccountkey="xxx" --type=Opaque
 ```

### Tips
  - mounting Azure SMB File share requires account key, if `nodeStageSecretRef` field is not provided in PV config, this driver would try to get `azure-storage-account-{accountname}-secret` in the pod namespace first, if that secret does not exist, it would get account key by Azure storage account API directly using kubelet identity (make sure kubelet identity has reader access to the storage account).
  - mounting Azure NFS File share does not need account key, NFS mount access is configured by either of the following settings:
    - `Firewalls and virtual networks`: select `Enabled from selected virtual networks and IP addresses` with same vnet as agent node
    - `Private endpoint connections`

#### `shareName` parameter supports following pv/pvc metadata conversion
> if `shareName` value contains following strings, it would be converted into corresponding pv/pvc name or namespace
 - `${pvc.metadata.name}`
 - `${pvc.metadata.namespace}`
 - `${pv.metadata.name}`
