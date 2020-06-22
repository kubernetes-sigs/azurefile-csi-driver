# Snapshot Example

#### Feature Status
 - Status: alpha
 - Minimum supported Kubernetes version: `1.17.0`
> [Volume snapshot](https://kubernetes-csi.github.io/docs/snapshot-controller.html) is beta(enabled by default) since Kubernetes `1.17.0`

### Create a StorageClass
```console
kubectl apply -f $GOPATH/src/github.com/kubernetes-sigs/azurefile-csi-driver/deploy/example/storageclass-azurefile-csi.yaml
```

### Create a PVC
```console
kubectl apply -f $GOPATH/src/github.com/kubernetes-sigs/azurefile-csi-driver/deploy/example/pvc-azurefile-csi.yaml
```

### Create a VolumeSnapshotClass
```console
kubectl apply -f $GOPATH/src/github.com/kubernetes-sigs/azurefile-csi-driver/deploy/example/snapshot/volumesnapshotclass-azurefile.yaml
```

## Create a VolumeSnapshot
```console
kubectl apply -f $GOPATH/src/github.com/kubernetes-sigs/azurefile-csi-driver/deploy/example/snapshot/volumesnapshot-azurefile.yaml
```

## Delete a VolumeSnapshot
```console
kubectl delete -f $GOPATH/src/github.com/kubernetes-sigs/azurefile-csi-driver/deploy/example/snapshot/volumesnapshot-azurefile.yaml
```

## Delete a VolumeSnapshotClass

```console
kubectl delete -f $GOPATH/src/github.com/kubernetes-sigs/azurefile-csi-driver/deploy/example/snapshot/volumesnapshotclass-azurefile.yaml
```
