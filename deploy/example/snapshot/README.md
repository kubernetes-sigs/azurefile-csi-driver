# Azure File Snapshot and Restore feature

> Restoring an NFS file share snapshot is supported starting from CSI driver version v1.33.4 or later.

### Limitations of Azure file **restore** feature
The snapshot restore data copy is performed by the CSI driver controller against the storage account's data plane endpoint (`<account>.file.<storage-endpoint-suffix>`, e.g. `file.core.windows.net` for Azure public cloud, `file.core.chinacloudapi.cn` for Azure China, `file.core.usgovcloudapi.net` for Azure US Government), so the controller must be able to reach that endpoint.

#### Storage account with public network access enabled from all networks
No extra configuration required.

#### Storage account with firewall, private endpoint or `publicNetworkAccess=Disabled`
Use `useDataPlaneAPI: "oauth"` in the `VolumeSnapshotClass` (and in the destination `StorageClass` if it also targets a private storage account). See [Using `useDataPlaneAPI: oauth` against private storage accounts](../../../docs/driver-parameters.md#using-usedataplaneapi-oauth-against-private-storage-accounts) for the full prerequisites (CSI driver v1.33.0+, `Storage File Data Privileged Contributor` role on the driver controller identity, `networkAcls.bypass` includes `AzureServices`).

Example `VolumeSnapshotClass` for private storage accounts:

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: csi-azurefile-vsc-oauth
driver: file.csi.azure.com
deletionPolicy: Delete
parameters:
  useDataPlaneAPI: "oauth"
```

> Note: The restored `PersistentVolumeClaim` will still be mounted on customer nodes via CIFS / NFS, which is not affected by `useDataPlaneAPI: oauth`. The node → storage network path must still be reachable (Private Endpoint into the node vnet, or storage account allowing the node subnet).

#### Self-managed CSI driver (not the AKS-managed add-on)
For self-managed installations, `useDataPlaneAPI: "oauth"` does not provide the trusted-service bypass. Either add the vnet hosting the driver controller pod to the storage account's allowed networks list, or set `Public network access` to `Enabled from all networks` during snapshot / restore.

## Install CSI Driver

Follow the [instructions](https://github.com/kubernetes-sigs/azurefile-csi-driver/blob/master/docs/install-csi-driver-master.md) to install snapshot driver.

### 1. Create source PVC and an example pod to write data 
```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-csi.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/pvc-azurefile-csi.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/nginx-pod-azurefile.yaml
```
 - Check source PVC
```console
$ kubectl exec nginx-azurefile -- ls /mnt/azurefile
outfile
```

### 2. Create a snapshot on source PVC
```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/snapshot/volumesnapshotclass-azurefile.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/snapshot/volumesnapshot-azurefile.yaml
```
 - Check snapshot Status
```console
$ kubectl describe volumesnapshot azurefile-volume-snapshot
Name:         azurefile-volume-snapshot
Namespace:    default
Labels:       <none>
Annotations:  API Version:  snapshot.storage.k8s.io/v1
Kind:         VolumeSnapshot
Metadata:
  Creation Timestamp:  2020-07-21T08:00:50Z
  Finalizers:
    snapshot.storage.kubernetes.io/volumesnapshot-as-source-protection
    snapshot.storage.kubernetes.io/volumesnapshot-bound-protection
  Generation:        1
  Resource Version:  16078
  Self Link:         /apis/snapshot.storage.k8s.io/v1/namespaces/default/volumesnapshots/azurefile-volume-snapshot
  UID:               d7a3a5fb-cf58-4e57-b561-f6d7a0d10d6d
Spec:
  Source:
    Persistent Volume Claim Name:  pvc-azurefile
  Volume Snapshot Class Name:      csi-azurefile-vsc
Status:
  Bound Volume Snapshot Content Name:  snapcontent-d7a3a5fb-cf58-4e57-b561-f6d7a0d10d6d
  Creation Time:                       2020-07-21T07:36:02Z
  Ready To Use:                        true
  Restore Size:                        100Gi
Events:                                <none>
```
> In above example, `snapcontent-2b0ef334-4112-4c86-8360-079c625d5562` is the snapshot name

### 3. Create a new PVC based on snapshot
```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/snapshot/pvc-azurefile-snapshot-restored.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/snapshot/nginx-pod-restored-snapshot.yaml
```

 - Check data
```console
$ kubectl exec nginx-restored -- ls /mnt/azurefile
lost+found
outfile
```

#### Links
 - [CSI Snapshotter](https://github.com/kubernetes-csi/external-snapshotter)
