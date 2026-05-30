# ModifyVolume feature example
 - Feature Status: GA from Kubernetes 1.34
 - Supported from Azure File CSI driver v1.35.0

To learn more about this feature, please refer to the [Kubernetes documentation for VolumeAttributesClass feature](https://kubernetes.io/docs/concepts/storage/volume-attributes-classes/).

## Supported parameters in `VolumeAttributesClass`
- `provisionedIOPS`: provisioned IOPS for file shares using the provisioned v2 billing model (PremiumV2 or StandardV2 SKUs)
- `provisionedBandwidth`: provisioned throughput in MiB/s for file shares using the provisioned v2 billing model

> Note: These parameters only apply to file shares created with PremiumV2 or StandardV2 SKUs (e.g., `PremiumV2_LRS`, `StandardV2_LRS`).

Here is an example of the `VolumeAttributesClass` used to update the IOPS and throughput on an Azure PremiumV2 file share:

```yaml
apiVersion: storage.k8s.io/v1
kind: VolumeAttributesClass
metadata:
  name: premiumv2-fileshare-class
driverName: file.csi.azure.com
parameters:
  provisionedIOPS: "5000"
  provisionedBandwidth: "200"
```

## Usage

### Create a StorageClass, PVC and pod
> The following example creates a PremiumV2_LRS file share

```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/modifyvolume/storageclass-azurefile-csi-premiumv2.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/modifyvolume/pvc-azurefile-csi.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/nginx-pod-azurefile.yaml
```

### Wait until the PVC reaches the Bound state and the pod is in the Running state.
```bash
kubectl get pvc pvc-azurefile
kubectl get pod nginx-azurefile
```

### Create a new VolumeAttributesClass
```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/modifyvolume/volumeattributesclass.yaml
kubectl get volumeattributesclass premiumv2-fileshare-class
```

### Update the existing PVC to refer to the newly created VolumeAttributesClass
```bash
kubectl patch pvc pvc-azurefile --patch '{"spec": {"volumeAttributesClassName": "premiumv2-fileshare-class"}}'
```

### Wait until the VolumeAttributesClass is applied to the volume
```bash
kubectl describe pvc pvc-azurefile
```

The events section should show `VolumeModifySuccessful` when the modification is complete.
