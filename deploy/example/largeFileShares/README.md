## EnableLargeFileShares Example

[enableLargeFileShares](https://github.com/kubernetes-sigs/azurefile-csi-driver/blob/master/docs/driver-parameters.md) driver parameter allows customers to create storage accounts with [large file Shares](https://docs.microsoft.com/en-us/azure/storage/files/storage-files-how-to-create-large-file-share?tabs=azure-portal)  enabled. These storage accounts enable the customers to create file shares that can scale up to 100 TiB.

### Example

1. Create a storage class with enableLargeFileShares set to true

```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/largeFileShares/storageclass-azurefile-largefileshares.yaml
```

2. Create a PVC with storage size 100TiB. Create a pod referring to this PVC.

```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/largeFileShares/pvc-azurefile-largefileshares.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/nginx-pod-azurefile.yaml
```

3. Verify the size of the filesystem

```console
kubectl exec -it nginx-azurefile -- df -h /mnt/azurefile

Filesystem                                                                                Size  Used Avail Use% Mounted on
//ff86842739bf1404f897153.file.core.windows.net/pvc-8c3a5d11-700b-4a4c-abce-3ba0792ed947  100T   64K  100T   1% /mnt/azurefile
```
