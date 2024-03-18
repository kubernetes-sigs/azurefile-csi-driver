## CSI driver example
> refer to [driver parameters](../../docs/driver-parameters.md) for more detailed usage

### Azure File Dynamic Provisioning
#### Option#1: create storage account by CSI driver
 - Create storage class using Azure file management API(by default)
```console
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-csi.yaml
```
 - Create storage class using Azure file data plane API to get better file operation performance
```console
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-large-scale.yaml
```
 > set `useDataPlaneAPI: "true"` in storage class `parameters` when creating > 100 file shares in parallel to prevent [storage resource provider throttling](https://docs.microsoft.com/en-us/azure/azure-resource-manager/management/azure-subscription-service-limits#storage-resource-provider-limits)
 >
#### Option#2: bring your own storage account (only for SMB protocol)
 - Use `kubectl create secret` to create `azure-secret` with existing storage account name and key
```console
kubectl create secret generic azure-secret --from-literal azurestorageaccountname=NAME --from-literal azurestorageaccountkey="KEY" --type=Opaque
```

 - create storage class referencing `azure-secret`
```console
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-secret.yaml
```

#### Create application
 - Create a statefulset with volume mount
```
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/statefulset.yaml
```

 - Execute `df -h` command in the container
```
kubectl exec -it statefulset-azurefile-0 -- df -h
```
<pre>
Filesystem                                                                Size  Used Avail Use% Mounted on
...
//f571xxx.file.core.windows.net/pvc-54caa11f-9e27-11e9-ba7b-0601775d3b69  1.0G  64K  1.0G  1%   /mnt/azurefile
...
</pre>

### AzureFile Static Provisioning(use an existing Azure file share)
#### Option#1: storage class
> make sure cluster identity could access to the file share
 - Download [Azure file CSI storage class](https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-existing-share.yaml), edit `resourceGroup`, `storageAccount`, `shareName` in storage class
```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: azurefile-csi
provisioner: file.csi.azure.com
parameters:
  resourceGroup: EXISTING_RESOURCE_GROUP_NAME  # optional, only set this when storage account is not in the same resource group as agent node
  storageAccount: EXISTING_STORAGE_ACCOUNT_NAME
  shareName: SHARE_NAME
reclaimPolicy: Delete
volumeBindingMode: Immediate
```

 - Create storage class and PVC
```console
kubectl create -f storageclass-azurefile-existing-share.yaml
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/pvc-azurefile-csi.yaml
```

#### Option#2: PV/PVC
 - Create a PV, download `pv-azurefile-csi.yaml` file and edit `shareName` in `volumeAttributes`
```console
wget https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/pv-azurefile-csi.yaml
#edit pv-azurefile-csi.yaml
kubectl create -f pv-azurefile-csi.yaml
```

 - Create a PVC
```console
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/pvc-azurefile-csi-static.yaml
```

 - make sure pvc is created and in `Bound` status after a while
```console
kubectl describe pvc pvc-azurefile
```

#### Create an application
```console
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/nginx-pod-azurefile.yaml
```

 - Execute `df -h` command in the container
```console
kubectl exec -it nginx-azurefile -- df -h
```
<pre>
Filesystem                                                                Size  Used Avail Use% Mounted on
...
//f571xxx.file.core.windows.net/pvc-54caa11f-9e27-11e9-ba7b-0601775d3b69  1.0G  64K  1.0G  1%   /mnt/azurefile
...
</pre>
In the above example, there is a `/mnt/azurefile` directory mounted as cifs filesystem.

#### Option#3: Inline volume
 >  - inline volume does not support nfs protocol
 >  - to avoid performance issue, use persistent volume instead of inline volume when numerous pods are accessing the same volume.
 - in below SMB protocol example, create `azure-secret` with existing storage account name and key in the same namespace as pod, both secret and pod are in `default` namespace
```console
kubectl create secret generic azure-secret --from-literal azurestorageaccountname=NAME --from-literal azurestorageaccountkey="KEY" --type=Opaque
```

 - download `nginx-pod-azurefile-inline-volume.yaml` file and edit `shareName`, `secretName`
```console
wget https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/nginx-pod-azurefile-inline-volume.yaml
#edit nginx-pod-azurefile-inline-volume.yaml
kubectl create -f nginx-pod-azurefile-inline-volume.yaml
```
