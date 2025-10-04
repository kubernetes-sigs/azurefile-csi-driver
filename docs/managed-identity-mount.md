# Mount Azure SMB file share with managed identity
 - Feature status: Preview
 - supported from CSI driver v1.34.0 on Linux node

This article demonstrates the process of mounting smb file share with user-assigned managed identity authentication only, without relying on account key authentication.
> by default you could leverage the built-in user assigned managed identity(kubelet identity) bound to the AKS agent node pool(with naming rule [`AKS Cluster Name-agentpool`](https://docs.microsoft.com/en-us/azure/aks/use-managed-identity#summary-of-managed-identities))

> if you have created your own managed identity, make sure the managed identity is bound to the agent node pool.

## Before you begin
 - Make sure the managed identity is granted the `Storage File Data SMB MI Admin` role on the storage account.
 > here is an example that uses Azure CLI commands to assign the `Storage File Data SMB MI Admin` role to the managed identity for the storage account. If the storage account is created by the driver(dynamic provisioning), you need to grant `Storage File Data SMB MI Admin` role on the resource group where the storage account is located

```bash
mid="$(az identity list -g "$resourcegroup" --query "[?name == 'managedIdentityName'].principalId" -o tsv)"
said="$(az storage account list -g "$resourcegroup" --query "[?name == '$storageaccountname'].id" -o tsv)"
az role assignment create --assignee-object-id "$mid" --role "Storage File Data SMB MI Admin" --scope "$said"
```

 - Retrieve the clientID of managed identity
 > skip this step if you are going to use kubelet identity since CSI driver will default to using the kubelet identity if `clientID` parameter is not provided in storage class or persisent volume.
```bash
clientID=`az identity list -g "$resourcegroup" --query "[?name == '$identityname'].clientId" -o tsv`
```
    
## Dynamic Provisioning
- Ensure that the identity of your cluster control plane has been assigned the `Storage Account Contributor role` for the storage account.
 > if the storage account is created by the driver, then you need to grant `Storage Account Contributor` role to the resource group where the storage account is located
 > AKS cluster control plane identity has assigned the `Contributor` role on the node resource group by default.

1. Create a storage class
    ```yml
    apiVersion: storage.k8s.io/v1
    kind: StorageClass
    metadata:
      name: azurefile-csi
    provisioner: file.csi.azure.com
    parameters:
      resourceGroup: EXISTING_RESOURCE_GROUP_NAME   # optional, node resource group by default if it's not provided
      storageAccount: EXISTING_STORAGE_ACCOUNT_NAME # optional, a new account will be created if it's not provided
      mountWithManagedIdentity: "true"
      # optional, clientID of the managed identity, kubelet identity would be used by default if it's not provided
      clientID: "xxxxx-xxxx-xxx-xxx-xxxxxxx"
    reclaimPolicy: Delete
    volumeBindingMode: Immediate
    allowVolumeExpansion: true
    mountOptions:
      - dir_mode=0777  # modify this permission if you want to enhance the security
      - file_mode=0777
      - uid=0
      - gid=0
      - mfsymlinks
      - cache=strict  # https://linux.die.net/man/8/mount.cifs
      - nosharesock  # reduce probability of reconnect race
      - actimeo=30  # reduce latency for metadata-heavy workload
      - nobrl  # disable sending byte range lock requests to the server
    ```

1. create a statefulset with volume mount
```bash
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/statefulset.yaml
```

## Static Provisioning

> bring your own storage account

1. create PV with your own account
    ```yml
    apiVersion: v1
    kind: PersistentVolume
    metadata:
      name: pv-azurefile
    spec:
      capacity:
        storage: 100Gi
      accessModes:
        - ReadWriteMany
      persistentVolumeReclaimPolicy: Retain
      storageClassName: azurefile-csi
      mountOptions:
        - dir_mode=0777  # modify this permission if you want to enhance the security
        - file_mode=0777
        - uid=0
        - gid=0
        - mfsymlinks
        - cache=strict  # https://linux.die.net/man/8/mount.cifs
        - nosharesock  # reduce probability of reconnect race
        - actimeo=30  # reduce latency for metadata-heavy workload
        - nobrl  # disable sending byte range lock requests to the server
      csi:
        driver: file.csi.azure.com
        # make sure volumeHandle is unique for every identical share in the cluster
        volumeHandle: "{resource-group-name}#{account-name}#{file-share-name}"
        volumeAttributes:
          resourceGroup: EXISTING_RESOURCE_GROUP_NAME   # optional, node resource group by default if it's not provided
          storageAccount: EXISTING_STORAGE_ACCOUNT_NAME # optional, a new account will be created if it's not provided
          shareName: EXISTING_FILE_SHARE_NAME
          mountWithManagedIdentity: "true"
          # optional, clientID of the managed identity, kubelet identity would be used by default if it's empty
          clientID: "xxxxx-xxxx-xxx-xxx-xxxxxxx"
    ```

1. create a pvc and a deployment with volume mount
    ```console
    kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/deployment.yaml
    ```
