## Driver Parameters

 - in-tree [kubernetes.io/azure-file](https://kubernetes.io/docs/concepts/storage/volumes/#azurefile) driver parameters

### Dynamic Provision

Name | Meaning | Example | Mandatory | Default value 
--- | --- | --- | --- | ---
skuName | Azure file storage account type (alias: `storageAccountType`) | `Standard_LRS`, `Standard_ZRS`, `Standard_GRS`, `Standard_RAGRS`, `Premium_LRS` | No | `Standard_LRS` <br><br> Note:  <br> 1. minimum file share size of Premium account type is `100GB`<br> 2.[`ZRS` account type](https://docs.microsoft.com/en-us/azure/storage/common/storage-redundancy#zone-redundant-storage) is supported in limited regions <br> 3. Premium files shares is currently only available for LRS
storageAccount | specify Azure storage account name| STORAGE_ACCOUNT_NAME | No | if empty, driver will find a suitable storage account that matches `skuName` in the same resource group; if a storage account name is provided, storage account must exist.
location | specify Azure storage account location | `eastus`, `westus`, etc. | No | if empty, driver will use the same location name as current k8s cluster
resourceGroup | specify the resource group in which Azure file share will be created | existing resource group name | No | if empty, driver will use the same resource group name as current k8s cluster
shareName | specify Azure file share name | existing or new Azure file name | No | if empty, driver will generate an Azure file share name
secretNamespace | specify the namespace of secret to store account key | `default`,`kube-system`,etc | No | `default`
tags | [tags](https://docs.microsoft.com/en-us/azure/azure-resource-manager/management/tag-resources) would be created in newly created storage account | tag format: 'foo=aaa,bar=bbb' | No | ""

 - account tags format created by dynamic provisioning
```
created-by: azure
```

 - file share name format created by dynamic provisioning(example)
```
kubernetes-dynamic-pvc-820e5f1f-258f-488e-9383-282667f85ad4
```

### Static Provisioning(bring your own file share)

Name | Meaning | Available Value | Mandatory | Default value
--- | --- | --- | --- | ---
shareName | disk name | | Yes |
secretName | disk resource ID | `/subscriptions/{sub-id}/resourcegroups/{group-name}/providers/microsoft.compute/disks/{disk-id}` | Yes |
readOnly | file system read only or not  | `true`, `false` | No | `false`
