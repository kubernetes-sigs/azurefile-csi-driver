## CSI driver debug tips
### case#1: volume create/delete issue
> If you are using [managed CSI driver on AKS](https://docs.microsoft.com/en-us/azure/aks/csi-storage-drivers), this step does not apply since the driver controller is not visible to the user.
 - find csi driver controller pod
> There could be multiple controller pods (only one pod is the leader), if there are no helpful logs, try to get logs from the leader controller pod.
```console
kubectl get po -o wide -n kube-system | grep csi-azurefile-controller
```
<pre>
NAME                                            READY   STATUS    RESTARTS   AGE     IP             NODE
csi-azurefile-controller-56bfddd689-dh5tk       5/5     Running   0          35s     10.240.0.19    k8s-agentpool-22533604-0
csi-azurefile-controller-56bfddd689-sl4ll       5/5     Running   0          35s     10.240.0.23    k8s-agentpool-22533604-1
</pre>

 - get pod description and logs
```console
kubectl describe pod csi-azurefile-controller-56bfddd689-dh5tk -n kube-system > csi-azurefile-controller-description.log
kubectl logs csi-azurefile-controller-56bfddd689-dh5tk -c azurefile -n kube-system > csi-azurefile-controller.log
```

### case#2: volume mount/unmount failed
 - find csi driver pod that does the actual volume mount/unmount
```console
kubectl get po -o wide -n kube-system | grep csi-azurefile-node
```
<pre>
NAME                                            READY   STATUS    RESTARTS   AGE     IP             NODE
csi-azurefile-node-cvgbs                        3/3     Running   0          7m4s    10.240.0.35    k8s-agentpool-22533604-1
csi-azurefile-node-dr4s4                        3/3     Running   0          7m4s    10.240.0.4     k8s-agentpool-22533604-0
</pre>

 - get pod description and logs
```console
kubectl logs csi-azurefile-node-cvgbs -c azurefile -n kube-system > csi-azurefile-node.log
# only collect following logs if there is driver crash issue
kubectl describe pod csi-azurefile-node-cvgbs -n kube-system > csi-azurefile-node-description.log
kubectl logs csi-azurefile-node-cvgbs -c node-driver-registrar -n kube-system > node-driver-registrar.log
```

 - check cifs mount inside driver
```console
kubectl exec -it csi-azurefile-node-dzm5d -n kube-system -c azurefile -- mount | grep cifs
```
<pre>
//accountname.file.core.windows.net/pvc-0dba3a14-dd27-4c85-9caf-7db566db621f on /var/lib/kubelet/plugins/kubernetes.io/csi/pv/pvc-0dba3a14-dd27-4c85-9caf-7db566db621f/globalmount type cifs (rw,relatime,vers=3.1.1,cache=strict,username=accountname,uid=0,forceuid,gid=0,forcegid,addr=20.150.50.136,file_mode=0777,dir_mode=0777,soft,persistenthandles,nounix,serverino,mapposix,mfsymlinks,rsize=1048576,wsize=1048576,bsize=1048576,echo_interval=60,actimeo=30)
//accountname.file.core.windows.net/pvc-0dba3a14-dd27-4c85-9caf-7db566db621f on /var/lib/kubelet/pods/7c7c539b-0a97-472f-bce1-27d7ab7bf3b6/volumes/kubernetes.io~csi/pvc-0dba3a14-dd27-4c85-9caf-7db566db621f/mount type cifs (rw,relatime,vers=3.1.1,cache=strict,username=accountname,uid=0,forceuid,gid=0,forcegid,addr=20.150.50.136,file_mode=0777,dir_mode=0777,soft,persistenthandles,nounix,serverino,mapposix,mfsymlinks,rsize=1048576,wsize=1048576,bsize=1048576,echo_interval=60,actimeo=30)
</pre>

 - check nfs mount inside driver
```console
kubectl exec -it csi-azurefile-node-dzm5d -n kube-system -c azurefile -- mount | grep nfs
```
<pre>
accountname.file.core.windows.net:/accountname/pvcn-46c357b2-333b-4c42-8a7f-2133023d6c48 on /var/lib/kubelet/plugins/kubernetes.io/csi/pv/pvc-46c357b2-333b-4c42-8a7f-2133023d6c48/globalmount type nfs4 (rw,relatime,vers=4.1,rsize=1048576,wsize=1048576,namlen=255,hard,proto=tcp,timeo=600,retrans=2,sec=sys,clientaddr=10.244.0.6,local_lock=none,addr=20.150.29.168)
accountname.file.core.windows.net:/accountname/pvcn-46c357b2-333b-4c42-8a7f-2133023d6c48 on /var/lib/kubelet/pods/7994e352-a4ee-4750-8cb4-db4fcf48543e/volumes/kubernetes.io~csi/pvc-46c357b2-333b-4c42-8a7f-2133023d6c48/mount type nfs4 (rw,relatime,vers=4.1,rsize=1048576,wsize=1048576,namlen=255,hard,proto=tcp,timeo=600,retrans=2,sec=sys,clientaddr=10.244.0.6,local_lock=none,addr=20.150.29.168)
</pre>

 - get cloud config file(`azure.json`) on Linux node
```console
kubectl exec -it csi-azurefile-node-dx94w -n kube-system -c azurefile -- cat /etc/kubernetes/azure.json
```

 - get cloud config file(`azure.json`) on Windows node
```console
kubectl exec -it csi-azurefile-node-win-xxxxx -n kube-system -c azurefile cmd
type c:\k\azure.json
```

 - get Windows csi-proxy logs inside driver
```console
kubectl exec -it csi-azurefile-node-win-xxxxx -n kube-system -c azurefile cmd
type c:\k\csi-proxy.err.log
```

#### Update driver version quickly by editing driver deployment directly
 - update controller deployment
```console
kubectl edit deployment csi-azurefile-controller -n kube-system
```
 - update daemonset deployment
```console
kubectl edit ds csi-azurefile-node -n kube-system
```
change below deployment config, e.g.
```console
        image: mcr.microsoft.com/oss/kubernetes-csi/azurefile-csi:v1.24.0
        imagePullPolicy: Always
```

### troubleshooting connection failure on agent node
> server address of sovereign cloud: accountname.file.core.chinacloudapi.cn
#### SMB
 - On Linux node
```console
mkdir /tmp/test
sudo mount -v -t cifs //accountname.file.core.windows.net/filesharename /tmp/test -o  username=accountname,password=accountkey,dir_mode=0777,file_mode=0777,cache=strict,actimeo=30
```

#### get os version on the node
```console
uname -a
```

<details><summary>
Get client-side logs on Linux node if there is mount error 
</summary>

```console
kubectl debug node/{node-name} --image=nginx
kubectl cp node-debugger-{node-name-xxxx}:/host/var/log/messages /tmp/messages
kubectl cp node-debugger-{node-name-xxxx}:/host/var/log/syslog /tmp/syslog
kubectl cp node-debugger-{node-name-xxxx}:/host/var/log/kern.log /tmp/kern.log
# after the logs have been collected, you can delete the debug pod
kubectl delete po node-debugger-{node-name-xxxx}
```
 
</details>

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

<details><summary>
Get client-side logs on Windows node if there is mount error 
</summary>

```console
Get SMBClient events from Event Viewer under following path:
  Application and Services Logs -> Microsoft -> Windows -> SMBClient
```
 
</details>

#### NFS
 
```console
mkdir /tmp/test
mount -v -t nfs -o vers=4,minorversion=1,sec=sys accountname.file.core.windows.net:/accountname/filesharename /tmp/test
```

 - [Troubleshoot Azure File mount issues on AKS](http://aka.ms/filemounterror)
 - [Troubleshoot Azure Files problems in Linux (SMB)](https://learn.microsoft.com/en-us/azure/storage/files/storage-troubleshoot-linux-file-connection-problems)

### Troubleshooting performance issues on Azure Files

#### AzFileDiagnostics script
 - [AzFileDiagnostics script for Linux](https://github.com/Azure-Samples/azure-files-samples/tree/master/AzFileDiagnostics/Linux)
 - [AzFileDiagnostics script for Windows](https://github.com/Azure-Samples/azure-files-samples/tree/master/AzFileDiagnostics/Windows)

#### File shares are being throttled and overall performance is slow 
Find out whether you have been throttled by looking at your Azure Monitor by following this documentation - [Link](https://docs.microsoft.com/en-us/azure/storage/files/storage-troubleshooting-files-performance#cause-1-share-was-throttled)

##### Standard Files

Enable [large file shares](https://docs.microsoft.com/azure/storage/files/storage-files-how-to-create-large-file-share?tabs=azure-portal) on your storage account. Large file shares support up to 10,000 IOPS per share at no extra cost on standard tier. To use a storage account with large file shares enabled, the 'enableLargeFileShares' driver parameter should be set to true. If this flag is set to true and a storage account with large file shares enabled doesn't exist, a new storage account will be created. The quota needs to be set to 100 TiB to get 10K IOPS. We recommend that you enable large file shares when creating the Storage Account. However, if you do enable this manually for a storage account at a later time, you will not need to remount the file share. Same storage account can have multiple large file shares.

##### Premium Files
Azure premium files follows provisioned model where IOPS and throughput are associated to the quota. See this article that explains the co-relation between share size and IOPS and throughput - [link](https://docs.microsoft.com/azure/storage/files/understanding-billing#provisioned-model). Increase the share quota by following this guide - [link](https://github.com/kubernetes-sigs/azurefile-csi-driver/tree/master/deploy/example/resize).

##### For more, refer to this doc for performance troubleshooting tips - [Link to performance troubleshooting tips](https://docs.microsoft.com/en-us/azure/storage/files/storage-troubleshooting-files-performance)

##### [Storage considerations for Azure Kubernetes Service (AKS)](https://learn.microsoft.com/en-us/azure/cloud-adoption-framework/scenarios/app-platform/aks/storage)
##### [Compare access to Azure Files, Blob Storage, and Azure NetApp Files with NFS](https://learn.microsoft.com/en-us/azure/storage/common/nfs-comparison#comparison)
