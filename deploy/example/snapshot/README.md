# Azure File Snapshot feature

> NOTE: Due to [Azure File snapshot restore API limitation](https://github.com/kubernetes-sigs/azurefile-csi-driver/issues/136), this driver only supports snapshot creation, snapshot could be restored from Azure portal or cli.

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

#### Links
 - [CSI Snapshotter](https://github.com/kubernetes-csi/external-snapshotter)
