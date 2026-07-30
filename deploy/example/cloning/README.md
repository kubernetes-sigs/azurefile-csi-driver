# Volume Cloning Example
## Feature Status: Beta

- supported from v1.28.6, v1.29.1
- SMB file share is supported, NFS file share is not supported
- ensure that you have granted the `Storage File Data Privileged Contributor` role to the CSI driver controller identity; otherwise, the driver will utilize an SAS key for volume cloning operations.

## Prerequisites

Volume cloning copies data through the storage account's data plane endpoint (`<account>.file.<storage-endpoint-suffix>`, e.g. `file.core.windows.net` for Azure public cloud, `file.core.chinacloudapi.cn` for Azure China, `file.core.usgovcloudapi.net` for Azure US Government), so the CSI driver controller must be able to reach that endpoint.

### Storage account with public network access enabled from all networks
No extra configuration required.

### Storage account with firewall, private endpoint or `publicNetworkAccess=Disabled`
Set `useDataPlaneAPI: "oauth"` in the source and destination `StorageClass`. See [Using `useDataPlaneAPI: oauth` against private storage accounts](../../../docs/driver-parameters.md#using-usedataplaneapi-oauth-against-private-storage-accounts) for the full prerequisites (CSI driver v1.33.0+, `Storage File Data Privileged Contributor` role on the driver controller identity, `networkAcls.bypass` includes `AzureServices`).

Example `StorageClass` snippet:

```yaml
parameters:
  useDataPlaneAPI: "oauth"
  # ... other parameters
```

> Note: The cloned `PersistentVolumeClaim` will still be mounted on customer nodes via CIFS, which is not affected by `useDataPlaneAPI: oauth`. The node → storage network path must still be reachable (Private Endpoint into the node vnet, or storage account allowing the node subnet).

### Self-managed CSI driver (not the AKS-managed add-on)
For self-managed installations, `useDataPlaneAPI: "oauth"` does not provide the trusted-service bypass. Either add the vnet hosting the driver controller pod to the storage account's allowed networks list, or set `Public network access` to `Enabled from all networks` during volume cloning.

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

