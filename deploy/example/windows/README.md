# CSI Driver on Windows

## Feature Status: GA
Refer to [Windows-CSI-Support](https://github.com/kubernetes/enhancements/blob/master/keps/sig-windows/20190714-windows-csi-support.md) for more details.

## Deploy a Windows pod with PVC mount
### Create storage class

```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/storageclass-azurefile-csi.yaml
```

### Create Windows pod
```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/windows/statefulset.yaml
```

### Enter pod container to do validation
```console
# kubectl exec -it busybox-azurefile-0 cmd
Microsoft Windows [Version 10.0.17763.1098]
(c) 2018 Microsoft Corporation. All rights reserved.

C:\>cd c:\mnt\azurefile

c:\mnt\azurefile>dir
 Volume in drive C has no label.
 Volume Serial Number is C820-6BEE

 Directory of c:\mnt\azurefile

05/31/2020  02:23 PM    <DIR>          .
05/31/2020  02:23 PM    <DIR>          ..
05/31/2020  02:23 PM               418 data.txt
               1 File(s)            418 bytes
               2 Dir(s)  107,374,116,864 bytes free

c:\mnt\azurefile>cat data.txt
2020-05-31 14:23:20Z
2020-05-31 14:23:22Z
2020-05-31 14:23:23Z
```

In the above example, there is a `c:\mnt\azurefile` directory mounted as NTFS filesystem.
