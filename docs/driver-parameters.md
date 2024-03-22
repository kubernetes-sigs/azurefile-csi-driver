## Driver Parameters
`file.csi.azure.com` driver parameters

<details><summary>required permissions for CSI driver controller</summary>
<pre>
Microsoft.Storage/storageAccounts/read
Microsoft.Storage/storageAccounts/write
Microsoft.Storage/storageAccounts/listKeys/action
Microsoft.Storage/operations/read
Microsoft.Network/virtualNetworks/subnets/write
Microsoft.Network/virtualNetworks/subnets/read
Microsoft.Network/virtualNetworks/subnets/*/read
Microsoft.Network/privateEndpoints/write
Microsoft.Network/privateEndpoints/read
Microsoft.Network/privateEndpoints/privateDnsZoneGroups/write
Microsoft.Network/privateDnsZones/write
Microsoft.Network/privateDnsZones/virtualNetworkLinks/write
Microsoft.Network/privateDnsZones/virtualNetworkLinks/read
Microsoft.Network/privateDnsZones/read
Microsoft.Network/privateDnsOperationStatuses/read
Microsoft.Network/locations/operations/read
</pre>
</details>

### Dynamic Provision
  > get a [example](../deploy/example/storageclass-azurefile-csi.yaml)

Name | Meaning | Example | Mandatory | Default value 
--- | --- | --- | --- | ---
skuName | Azure file storage account type (alias: `storageAccountType`) | `Standard_LRS`, `Standard_ZRS`, `Standard_GRS`, `Standard_RAGRS`, `Standard_RAGZRS`, `Premium_LRS`, `Premium_ZRS` | No | `Standard_LRS` <br><br> Note:  <br> 1. minimum file share size of Premium account type is `100GB`<br> 2.[`ZRS` account type](https://docs.microsoft.com/en-us/azure/storage/common/storage-redundancy#zone-redundant-storage) is supported in limited regions <br> 3. NFS file share only supports Premium account type
storageAccount | specify Azure storage account name| STORAGE_ACCOUNT_NAME | No | If the driver is not provided with a specific storage account name, it will search for a suitable storage account that matches the account settings within the same resource group. If it cannot find a matching storage account, it will create a new one. However, if a storage account name is specified, the storage account must already exist.
enableLargeFileShares | specify whether to use a storage account with large file shares enabled or not. If this flag is set to true and a storage account with large file shares enabled doesn't exist, a new storage account with large file shares enabled will be created. This flag should be used with the standard sku as the storage accounts created with premium sku have largeFileShares option enabled by default.  | `true`,`false` | No | `false`
protocol | file share protocol | `smb`, `nfs` | No | `smb`
networkEndpointType | specify network endpoint type for the storage account created by driver. If `privateEndpoint` is specified, a private endpoint will be created for the storage account. For other cases, a service endpoint will be created by default. | "",`privateEndpoint` | No | `` <br>for AKS cluster, make sure cluster Control plane identity (that is, your AKS cluster name) is added to the Contributor role in the resource group hosting the VNet
location | specify Azure storage account location | `eastus`, `westus`, etc. | No | if empty, driver will use the same location name as current k8s cluster
resourceGroup | specify the resource group in which Azure file share will be created | existing resource group name | No | if empty, driver will use the same resource group name as current k8s cluster
shareName | specify Azure file share name | existing or new Azure file name | No | if empty, driver will generate an Azure file share name
shareNamePrefix | specify Azure file share name prefix created by driver | can only contain lowercase letters, numbers, hyphens, and length should be less than 21 | No |
folderName | specify folder name in Azure file share | existing folder name in Azure file share | No | if folder name does not exist in file share, mount would fail
shareAccessTier | [Access tier for file share](https://docs.microsoft.com/en-us/azure/storage/files/storage-files-planning#storage-tiers) (this parameter is ignored when using bring your own account key scenario) | For general-purpose v2 account, the available tiers are `TransactionOptimized`(default), `Hot`, and `Cool`. For file storage account, the available tier is `Premium`. | No | empty(use default setting for different storage account types)
server | specify Azure storage account server address | existing server address, e.g. `accountname.privatelink.file.core.windows.net` | No | if empty, driver will use default `accountname.file.core.windows.net` or other sovereign cloud account address
disableDeleteRetentionPolicy | specify whether disable DeleteRetentionPolicy for storage account created by driver | `true`,`false` | No | `false`
allowBlobPublicAccess | Allow or disallow public access to all blobs or containers for storage account created by driver | `true`,`false` | No | `false`
requireInfraEncryption | specify whether or not the service applies a secondary layer of encryption with platform managed keys for data at rest for storage account created by driver | `true`,`false` | No | `false`
storageEndpointSuffix | specify Azure storage endpoint suffix | `core.windows.net`, `core.chinacloudapi.cn`, etc | No | if empty, driver will use default storage endpoint suffix according to cloud environment, e.g. `core.windows.net`
tags | [tags](https://docs.microsoft.com/en-us/azure/azure-resource-manager/management/tag-resources) would be created in newly created storage account | tag format: 'foo=aaa,bar=bbb' | No | ""
matchTags | whether matching tags when driver tries to find a suitable storage account | `true`,`false` | No | `false`
selectRandomMatchingAccount | whether randomly selecting a matching account, by default, the driver would always select the first matching account in alphabetical order(note: this driver uses account search cache, which results in uneven distribution of file creation across multiple accounts) | `true`,`false` | No | `false`
accountQuota | to limit the quota for an account, you can specify a maximum quota in GB (`102400`GB by default). If the account exceeds the specified quota, the driver would skip selecting the account | `` | No | `102400`
--- | **Following parameters are only for SMB protocol** | --- | --- |
subscriptionID | specify Azure subscription ID where Azure file share will be created | Azure subscription ID | No | if not empty, `resourceGroup` must be provided
storeAccountKey | whether store account key to k8s secret <br><br> Note:  <br> `false` means driver would leverage kubelet identity to get account key | `true`,`false` | No | `true`
getLatestAccountKey | whether getting the latest account key based on the creation time, this driver would get the first key by default | `true`,`false` | No | `false`
secretName | specify secret name to store account key | | No |
secretNamespace | specify the namespace of secret to store account key | `default`,`kube-system`, etc | No | pvc namespace (`csi.storage.k8s.io/pvc/namespace`)
useDataPlaneAPI | specify whether use [data plane API](https://github.com/Azure/azure-sdk-for-go/blob/master/storage/share.go) for file share create/delete/resize, this could solve the SRP API throttling issue since data plane API has almost no limit, while it would fail when there is firewall or vnet setting on storage account | `true`,`false` | No | `false`
enableMultichannel | specify whether enable [SMB multi-channel](https://learn.microsoft.com/en-us/azure/storage/files/files-smb-protocol?tabs=azure-portal#smb-multichannel) for **Premium** storage account <br> Note: this feature is used with `max_channels=4` (or 2,3) mount option | `true`,`false` | No | `false`
--- | **Following parameters are only for NFS protocol** | --- | --- |
rootSquashType | specify root squashing behavior on the share. The default is `NoRootSquash` | `AllSquash`, `NoRootSquash`, `RootSquash` | No |
mountPermissions | mounted folder permissions. The default is `0777`, if set as `0`, driver will not perform `chmod` after mount | `0777` | No |
--- | **Following parameters are only for vnet setting, e.g. NFS, private end point** | --- | --- |
vnetResourceGroup | specify vnet resource group where virtual network is | existing resource group name | No | if empty, driver will use the `vnetResourceGroup` value in azure cloud config file
vnetName | virtual network name | existing virtual network name | No | if empty, driver will use the `vnetName` value in azure cloud config file
subnetName | subnet name | existing subnet name(s) of the agent node, if you want to update service endpoints on multiple subnets, separate them using a comma (`,`) | No | if empty, driver will use the `subnetName` value in azure cloud config file
fsGroupChangePolicy | indicates how volume's ownership will be changed by the driver, pod `securityContext.fsGroupChangePolicy` is ignored  | `OnRootMismatch`(by default), `Always`, `None` | No | `OnRootMismatch`

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
volumeHandle | Specify a value the driver can use to uniquely identify the share in the cluster. | A recommended way to produce a unique value is to combine the globally unique storage account name and share name: {account-name}_{file-share-name}. If you plan to use resize, you must follow the VolumeID format in Dynamic Provisioning. Note: The # character is reserved for internal use. | Yes |
volumeAttributes.subscriptionID | specify Azure subscription ID where Azure file share is located | Azure subscription ID | No | if not empty, `resourceGroup` must be provided
volumeAttributes.resourceGroup | Azure resource group name | existing resource group name | No | if empty, driver will use the same resource group name as current k8s cluster
volumeAttributes.storageAccount | existing storage account name | existing storage account name | Yes |
volumeAttributes.shareName | Azure file share name | existing Azure file share name | Yes |
volumeAttributes.folderName | specify folder name in Azure file share | existing folder name in Azure file share | No | if folder name does not exist in file share, mount would fail
volumeAttributes.protocol | specify file share protocol | `smb`, `nfs` | No | `smb`
volumeAttributes.server | specify Azure storage account server address | existing server address, e.g. `accountname.privatelink.file.core.windows.net` | No | if empty, driver will use default `accountname.file.core.windows.net` or other sovereign cloud account address
volumeAttributes.storageEndpointSuffix | specify Azure storage endpoint suffix | `core.windows.net`, `core.chinacloudapi.cn`, etc | No | if empty, driver will use default storage endpoint suffix according to cloud environment, e.g. `core.windows.net`
--- | **Following parameters are only for SMB protocol** | --- | --- |
volumeAttributes.secretName | secret name that stores storage account name and key | | No |
volumeAttributes.secretNamespace | secret namespace | `default`,`kube-system`, etc | No | pvc namespace (`csi.storage.k8s.io/pvc/namespace`)
volumeAttributes.getLatestAccountKey | whether getting the latest account key based on the creation time, this driver would get the first key by default | `true`,`false` | No | `false`
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
  - mounting Azure SMB File share requires account key
    - set `storeAccountKey: "false"` in storage class would make driver **not** store account key as k8s secret
    - if the `nodeStageSecretRef` field is not specified in the persistent volume (PV) configuration, the driver will attempt to retrieve the `azure-storage-account-{accountname}-secret` in the pod namespace. 
    - If `azure-storage-account-{accountname}-secret` in the pod namespace does not exist, the driver will use the kubelet identity to retrieve the account key directly from the Azure storage account API, provided that the kubelet identity has reader access to the storage account.
  - mounting Azure NFS File share does not require account key, NFS mount access is configured by either of the following settings:
    - `Firewalls and virtual networks`: select `Enabled from selected virtual networks and IP addresses` with same vnet as agent node
    - `Private endpoint connections`
  - If a storage account is full, the driver will add a tag `skip-matching` to the storage account to prevent the creation of new file shares in that account. This tag will only be removed after 30 minutes if a file share is deleted from that account. To use the account immediately, the user can manually remove the tag.
  - The default NFS mount options in this driver are `vers=4,minorversion=1,sec=sys`. It is not supported to specify these NFS mount options, including `nfsvers`.

#### `shareName` parameter supports following pv/pvc metadata conversion
> if `shareName` value contains following strings, it would be converted into corresponding pv/pvc name or namespace
 - `${pvc.metadata.name}`
 - `${pvc.metadata.namespace}`
 - `${pv.metadata.name}`

#### [Storage considerations for Azure Kubernetes Service (AKS)](https://learn.microsoft.com/en-us/azure/cloud-adoption-framework/scenarios/app-platform/aks/storage)
#### [Compare access to Azure Files, Blob Storage, and Azure NetApp Files with NFS](https://learn.microsoft.com/en-us/azure/storage/common/nfs-comparison#comparison)
