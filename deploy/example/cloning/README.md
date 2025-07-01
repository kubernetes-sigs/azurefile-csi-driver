# Volume Cloning Example
## Feature Status: Beta

- supported from v1.28.6, v1.29.1
- SMB file share is supported, NFS file share is not supported
- use of private endpoints is not supported due to the `DenyAll` default network rules in the storage account's VNet settings
- ensure that you have granted the `Storage File Data Privileged Contributor` role to the CSI driver controller identity; otherwise, the driver will utilize an SAS key for volume cloning operations.

## Prerequisites
- make sure that the virtual network hosting the driver controller pod is added to the list of allowed virtual networks in the storage account's VNet settings
  - if the driver controller pod is managed by AKS, you need to set `Enable from all networks` in the storage account's VNet settings

## Create a Source PVC

```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-csi.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/pvc-azurefile-csi.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/nginx-pod-azurefile.yaml
```

### Check the Source PVC

```console
$ kubectl exec nginx-azurefile -- ls /mnt/azurefile
outfile
```

## Create a PVC from an existing PVC
>  Make sure application is not writing data to source fileshare
```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/cloning/pvc-azurefile-cloning.yaml
```
### Check the Creation Status

```console
$ kubectl describe pvc pvc-azurefile-cloning
Name:          pvc-azurefile-cloning
Namespace:     default
StorageClass:  azurefile-csi
Status:        Bound
Volume:        pvc-bcbc953a-0232-457b-9100-6f1305c48b85
Labels:        <none>
Annotations:   pv.kubernetes.io/bind-completed: yes
               pv.kubernetes.io/bound-by-controller: yes
               volume.beta.kubernetes.io/storage-provisioner: file.csi.azure.com
               volume.kubernetes.io/storage-provisioner: file.csi.azure.com
Finalizers:    [kubernetes.io/pvc-protection]
Capacity:      100Gi
Access Modes:  RWX
VolumeMode:    Filesystem
DataSource:
  Kind:   PersistentVolumeClaim
  Name:   pvc-azurefile
Used By:  <none>
Events:
  Type     Reason                 Age                    From                                                                                       Message
  ----     ------                 ----                   ----                                                                                       -------
  Normal   ExternalProvisioning   4m41s (x2 over 4m54s)  persistentvolume-controller                                                                waiting for a volume to be created, either by external provisioner "file.csi.azure.com" or manually created by system administrator
  Normal   Provisioning           4m38s (x5 over 4m54s)  file.csi.azure.com_aks-nodepool1-34988195-vmss000002_a240766c-7d4d-47f1-8f91-d97abbecad49  External provisioner is provisioning volume for claim "default/pvc-azurefile-cloning"
  Normal   ProvisioningSucceeded  4m30s                  file.csi.azure.com_aks-nodepool1-34988195-vmss000002_a240766c-7d4d-47f1-8f91-d97abbecad49  Successfully provisioned volume pvc-bcbc953a-0232-457b-9100-6f1305c48b85
```

## Restore the PVC into a Pod

```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/cloning/nginx-pod-restored-cloning.yaml
```

### Check Sample Data

```console
$ kubectl exec nginx-azurefile-restored-cloning -- ls /mnt/azurefile
outfile
```

