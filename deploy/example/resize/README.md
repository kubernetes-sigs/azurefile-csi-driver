# Volume Resizing Support

## Example

1. Set `allowVolumeExpansion` field as true in the storageclass manifest.  

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: azurefile-csi
provisioner: file.csi.azure.com
allowVolumeExpansion: true
parameters:
  skuName: Standard_LRS
reclaimPolicy: Delete
volumeBindingMode: Immediate
mountOptions:
  - dir_mode=0777
  - file_mode=0777
  - mfsymlinks
  - cache=strict  # https://linux.die.net/man/8/mount.cifs
  - nosharesock  # reduce probability of reconnect race
  - actimeo=30  # reduce latency for metadata-heavy workload
  - nobrl  # disable sending byte range lock requests to the server and for applications which have challenges with posix locks
```

2. Create storageclass, pvc and pod.

```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-csi.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/pvc-azurefile-csi.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/nginx-pod-azurefile.yaml
```

3. Check the PV size
```console
kubectl get pvc pvc-azurefile
```
<pre>
NAME            STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS         AGE
pvc-azurefile   Bound    pvc-74dc3e29-534f-4d54-98fc-731adb46c948   15Gi       RWX            file.csi.azure.com   57m
</pre>

4. Check the filesystem size in the container.

```console
kubectl exec -it nginx-azurefile -- df -h /mnt/azurefile
```
<pre>
Filesystem                                                                                Size  Used Avail Use% Mounted on
//fuse0575b5cff3b641d7a0c.file.core.windows.net/pvc-74dc3e29-534f-4d54-98fc-731adb46c948   15G  128K   15G   1% /mnt/azurefile
</pre>

4. Expand the pvc by increasing the field `spec.resources.requests.storage`.

```console
kubectl edit pvc pvc-azurefile
```
<pre>
...
...
spec:
  resources:
    requests:
      storage: 20Gi
...
...
</pre>

5. Verify the filesystem size.

```console
kubectl get pvc pvc-azurefile
```
<pre>
NAME            STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS         AGE
pvc-azurefile   Bound    pvc-74dc3e29-534f-4d54-98fc-731adb46c948   20Gi       RWX            file.csi.azure.com   65m
</pre>

```
kubectl exec -it nginx-azurefile -- df -h /mnt/azurefile
```
<pre>
Filesystem                                                                                Size  Used Avail Use% Mounted on
//fuse0575b5cff3b641d7a0c.file.core.windows.net/pvc-74dc3e29-534f-4d54-98fc-731adb46c948   20G  128K   20G   1% /mnt/azurefile
</pre>