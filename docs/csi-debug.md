## CSI driver debug tips
### Case#1: volume create/delete issue
 - locate csi driver pod
```console
$ kubectl get po -o wide -n kube-system | grep csi-azurefile-controller
NAME                                            READY   STATUS    RESTARTS   AGE     IP             NODE
csi-azurefile-controller-56bfddd689-dh5tk       5/5     Running   0          35s     10.240.0.19    k8s-agentpool-22533604-0
csi-azurefile-controller-56bfddd689-sl4ll       5/5     Running   0          35s     10.240.0.23    k8s-agentpool-22533604-1
```
 - get csi driver logs
```console
$ kubectl logs csi-azurefile-controller-56bfddd689-dh5tk -c azurefile -n kube-system > csi-azurefile-controller.log
```
> note: there could be multiple controller pods, if there are no helpful logs, try to get logs from other controller pods

### Case#2: volume mount/unmount failed
 - locate csi driver pod and make sure which pod do tha actual volume mount/unmount
```console
$ kubectl get po -o wide -n kube-system | grep csi-azurefile-node
NAME                                            READY   STATUS    RESTARTS   AGE     IP             NODE
csi-azurefile-node-cvgbs                        3/3     Running   0          7m4s    10.240.0.35    k8s-agentpool-22533604-1
csi-azurefile-node-dr4s4                        3/3     Running   0          7m4s    10.240.0.4     k8s-agentpool-22533604-0
```

 - get csi driver logs
```console
$ kubectl logs csi-azurefile-node-cvgbs -c azurefile -n kube-system > csi-azurefile-node.log
```

### troubleshooting connection failure on agent node
> server address of sovereign cloud: accountname.blob.core.chinacloudapi.cn
##### SMB
 - On Linux node
```console
mkdir /tmp/test
sudo mount -v -t cifs //accountname.blob.core.windows.net/filesharename /tmp/test -o vers=3.0,username=accountname,password=accountkey,dir_mode=0777,file_mode=0777,cache=strict,actimeo=30
```

 - On Windows node
```console
$User = "AZURE\accountname"
$PWord = ConvertTo-SecureString -String "xxx" -AsPlainText -Force
$Credential = New-Object –TypeName System.Management.Automation.PSCredential –ArgumentList $User, $Pword
New-SmbGlobalMapping -LocalPath x: -RemotePath \\accountname.file.core.windows.net\sharename -Credential $Credential
Get-SmbGlobalMapping
cd x:
dir
```

 - NFSv4
 
```console
mkdir /tmp/test
mount -v -t nfs -o vers=4,minorversion=1,sec=sys accountname.blob.core.windows.net:/accountname/filesharename /tmp/test
```


### Troubleshooting performance issues on Azure Files

##### File shares are being throttled and overall performance is slow 
Find out whether you have been throttled by looking at your Azure Monitor by following this documentation - [Link](https://docs.microsoft.com/en-us/azure/storage/files/storage-troubleshooting-files-performance#cause-1-share-was-throttled)

###### Standard Files

Enable [large file shares](https://docs.microsoft.com/azure/storage/files/storage-files-how-to-create-large-file-share?tabs=azure-portal) on your storage account. Large file shares support up to 10,000 IOPS per share at no extra cost on standard tier. To use a storage account with large file shares enabled, the 'enableLargeFileShares' driver parameter should be set to true. If this flag is set to true and a storage account with large file shares enabled doesn't exist, a new storage account will be created. The quota needs to be set to 100 TiB to get 10K IOPS. We recommend that you enable large file shares when creating the Storage Account. However, if you do enable this manually for a storage account at a later time, you will not need to remount the file share. Same storage account can have multiple large file shares.

##### Premium Files
Azure premium files follows provisioned model where IOPS and throughput are associated to the quota. See this article that explains the co-relation between share size and IOPS and throughput - [link](https://docs.microsoft.com/azure/storage/files/understanding-billing#provisioned-model). Increase the share quota by following this guide - [link](https://github.com/kubernetes-sigs/azurefile-csi-driver/tree/master/deploy/example/resize).

##### For more, refer to this doc for perforance troubleshooting tips - [Link to performance troubleshooting tips](https://docs.microsoft.com/en-us/azure/storage/files/storage-troubleshooting-files-performance)
