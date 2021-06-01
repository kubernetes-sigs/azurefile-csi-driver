## Mount on-premise SMB server

This driver could not only mount Azure File share, it also supports mount on-premise SMB server as long as agent node could access the target SMB server.

1. Create a Kubernetes secret to store on-premise SMB server username and password
```console
kubectl create secret -ns teste generic smbcreds --from-literal azurestorageaccountname=USERNAME --from-literal azurestorageaccountkey="PASSWORD"
```

2. Create an inline volume pod with on-premise SMB server mount
 > download `nginx-on-prem-volume.yaml`, edit `server`, `shareName`, `secretName`
```console
wget https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/smb-provisioner/nginx-on-prem-volume.yaml
# edit nginx-on-prem-volume.yaml
kubectl apply -f nginx-on-prem-volume.yaml
```

3. Check on-prem smb mount
```console
kubectl exec -it nginx-on-prem-volume -- df -h
```
<pre>
Filesystem                                    Size  Used Avail Use% Mounted on
...
//smb-server.default.svc.cluster.local/share  124G   45G   80G  37% /mnt/smb
</pre>
