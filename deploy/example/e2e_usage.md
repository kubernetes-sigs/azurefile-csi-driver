## CSI driver example
> refer to [driver parameters](../../docs/driver-parameters.md) for more detailed usage

### AzureFile Dynamic Provisioning
 - Create a CSI storage class
```console
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-csi.yaml
```

 - Create a statefulset with Azure Disk mount
```
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/statefulset.yaml
```

 - Execute `df -h` command in the container
```
# kubectl exec -it statefulset-azurefile-0 sh
# df -h
Filesystem                                                                Size  Used Avail Use% Mounted on
...
//f571xxx.file.core.windows.net/pvc-54caa11f-9e27-11e9-ba7b-0601775d3b69  1.0G  64K  1.0G  1%   /mnt/azurefile
...
```

### AzureFile Static Provisioning(use an existing Azure file share)
#### Option#1: Use storage class
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

#### Option#2: Use secret
 - Use `kubectl create secret` to create `azure-secret` with existing storage account name and key
```console
kubectl create secret generic azure-secret --from-literal accountname=NAME --from-literal accountkey="KEY" --type=Opaque
```

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

#### create a pod with PVC mount
```console
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/nginx-pod-azurefile.yaml
```

 - Execute `df -h` command in the container
```console
$ kubectl exec -it nginx-azurefile -- bash
root@nginx-azurefile:/# df -h
Filesystem                                                                Size  Used Avail Use% Mounted on
...
//f571xxx.file.core.windows.net/pvc-54caa11f-9e27-11e9-ba7b-0601775d3b69  1.0G  64K  1.0G  1%   /mnt/azurefile
...
```
In the above example, there is a `/mnt/azurefile` directory mounted as cifs filesystem.
