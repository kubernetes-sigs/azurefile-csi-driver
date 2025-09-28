# workload identity support on static provisioning
 - supported from v1.30.1 (from AKS 1.29 with `tokenRequests` field support in `CSIDriver`)

### Limitations
 - This feature is not supported for NFS mount since NFS mount does not need credentials.
 - This feature would still retrieve storage account key using federated identity credentials and mount Azure File share using key-based authentication.

## Prerequisites
### 1. Create a cluster with oidc-issuer enabled and get the AKS cluster credential
Refer to the [documentation](https://learn.microsoft.com/en-us/azure/aks/use-oidc-issuer#create-an-aks-cluster-with-oidc-issuer) for instructions on creating a new AKS cluster with the `--enable-oidc-issuer` parameter and get the AKS credentials. And export following environment variables:
```console
export RESOURCE_GROUP=<your resource group name>
export CLUSTER_NAME=<your cluster name>
export REGION=<your region>
```

### 2. Bring your own storage account and file share
Refer to the [documentation](https://learn.microsoft.com/en-us/azure/storage/files/storage-how-to-use-files-portal?tabs=azure-cli) for instructions on creating a new storage account and file share, or alternatively, utilize your existing storage account and file share. And export following environment variables:
```console
export STORAGE_RESOURCE_GROUP=<your storage account resource group>
export ACCOUNT=<your storage account name>
export SHARE=<your fileshare name> # optional
```

### 3. Create or bring your own managed identity and grant role to the managed identity
> you could leverage the default user assigned managed identity bound to the AKS agent node pool(with naming rule [`AKS Cluster Name-agentpool`](https://docs.microsoft.com/en-us/azure/aks/use-managed-identity#summary-of-managed-identities)) in node resource group
```console
export UAMI=<your managed identity name>
az identity create --name $UAMI --resource-group $RESOURCE_GROUP

export USER_ASSIGNED_CLIENT_ID="$(az identity show -g $RESOURCE_GROUP --name $UAMI --query 'clientId' -o tsv)"
export IDENTITY_TENANT=$(az aks show --name $CLUSTER_NAME --resource-group $RESOURCE_GROUP --query identity.tenantId -o tsv)
export ACCOUNT_SCOPE=$(az storage account show --name $ACCOUNT --query id -o tsv)
```
 - grant `Storage Account Contributor` role to the managed identity to retrieve account key (default)
```console
az role assignment create --role "Storage Account Contributor" --assignee $USER_ASSIGNED_CLIENT_ID --scope $ACCOUNT_SCOPE
```

 - grant the `Storage Blob Data Contributor` role to the managed identity for mounting using a workload identity token exclusively, without relying on account key authentication.
```console
az role assignment create --role "Storage Account Contributor" --assignee $USER_ASSIGNED_CLIENT_ID --scope $ACCOUNT_SCOPE
```

### 4. Create a service account on AKS
```console
export SERVICE_ACCOUNT_NAME=<your sa name>
export SERVICE_ACCOUNT_NAMESPACE=<your sa namespace>

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${SERVICE_ACCOUNT_NAME}
  namespace: ${SERVICE_ACCOUNT_NAMESPACE}
EOF
```

### 5. Create the federated identity credential between the managed identity, service account issuer, and subject using the `az identity federated-credential create` command.
```console
export FEDERATED_IDENTITY_NAME=<your federated identity name>
export AKS_OIDC_ISSUER="$(az aks show --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME --query "oidcIssuerProfile.issuerUrl" -o tsv)"

az identity federated-credential create --name $FEDERATED_IDENTITY_NAME \
--identity-name $UAMI \
--resource-group $RESOURCE_GROUP \
--issuer $AKS_OIDC_ISSUER \
--subject system:serviceaccount:${SERVICE_ACCOUNT_NAMESPACE}:${SERVICE_ACCOUNT_NAME}
```

## option#1: dynamic provisioning with storage class
```yaml
cat <<EOF | kubectl apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: azurefile-csi
provisioner: file.csi.azure.com
parameters:
  storageaccount: $ACCOUNT # required
  clientID: $USER_ASSIGNED_CLIENT_ID # required
  resourcegroup: $STORAGE_RESOURCE_GROUP # optional, specified when the storage account is not under AKS node resource group(which is prefixed with "MC_")
reclaimPolicy: Delete
volumeBindingMode: Immediate
allowVolumeExpansion: true
mountOptions:
  - dir_mode=0777  # modify this permission if you want to enhance the security
  - file_mode=0777
  - mfsymlinks
  - cache=strict  # https://linux.die.net/man/8/mount.cifs
  - nosharesock  # reduce probability of reconnect race
  - actimeo=30  # reduce latency for metadata-heavy workload
  - nobrl  # disable sending byte range lock requests to the server and for applications which have challenges with posix locks
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: statefulset-azurefile
  namespace: ${SERVICE_ACCOUNT_NAMESPACE}
  labels:
    app: nginx
spec:
  serviceName: statefulset-azurefile
  replicas: 1
  template:
    metadata:
      labels:
        app: nginx
    spec:
      serviceAccountName: $SERVICE_ACCOUNT_NAME  #required, make sure the pod have the required permissions to mount volume
      nodeSelector:
        "kubernetes.io/os": linux
      containers:
        - name: statefulset-azurefile
          image: mcr.microsoft.com/mirror/docker/library/nginx:1.23
          command:
            - "/bin/bash"
            - "-c"
            - set -euo pipefail; while true; do echo $(date) >> /mnt/azurefile/outfile; sleep 1; done
          volumeMounts:
            - name: persistent-storage
              mountPath: /mnt/azurefile
  updateStrategy:
    type: RollingUpdate
  selector:
    matchLabels:
      app: nginx
  volumeClaimTemplates:
    - metadata:
        name: persistent-storage
      spec:
        storageClassName: azurefile-csi
        accessModes: ["ReadWriteMany"]
        resources:
          requests:
            storage: 100Gi
EOF
```

## option#2: static provision with PV
```yaml
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: PersistentVolume
metadata:
  annotations:
    pv.kubernetes.io/provisioned-by: file.csi.azure.com
  name: pv-azurefile
spec:
  capacity:
    storage: 10Gi
  accessModes:
    - ReadWriteMany
  persistentVolumeReclaimPolicy: Retain
  storageClassName: azurefile-csi
  mountOptions:
    - dir_mode=0777  # modify this permission if you want to enhance the security
    - file_mode=0777
    - mfsymlinks
    - cache=strict  # https://linux.die.net/man/8/mount.cifs
    - nosharesock  # reduce probability of reconnect race
    - actimeo=30  # reduce latency for metadata-heavy workload
    - nobrl  # disable sending byte range lock requests to the server and for applications which have challenges with posix locks
  csi:
    driver: file.csi.azure.com
    # make sure volumeHandle is unique for every identical share in the cluster
    volumeHandle: "{resource-group-name}#{account-name}#{file-share-name}"
    volumeAttributes:
      storageaccount: $ACCOUNT # required
      shareName: $SHARE  # required
      clientID: $USER_ASSIGNED_CLIENT_ID # required
      resourcegroup: $STORAGE_RESOURCE_GROUP # optional, specified when the storage account is not under AKS node resource group(which is prefixed with "MC_")
---
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: pvc-azurefile
  namespace: ${SERVICE_ACCOUNT_NAMESPACE}
spec:
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 10Gi
  volumeName: pv-azurefile
  storageClassName: azurefile-csi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app: nginx
  name: deployment-azurefile
  namespace: ${SERVICE_ACCOUNT_NAMESPACE}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
      name: deployment-azurefile
    spec:
      serviceAccountName: $SERVICE_ACCOUNT_NAME  #required, Pod lacks the necessary permission to mount the volume without this field
      nodeSelector:
        "kubernetes.io/os": linux
      containers:
        - name: deployment-azurefile
          image: mcr.microsoft.com/oss/nginx/nginx:1.17.3-alpine
          command:
            - "/bin/sh"
            - "-c"
            - while true; do echo $(date) >> /mnt/azurefile/outfile; sleep 1; done
          volumeMounts:
            - name: azurefile
              mountPath: "/mnt/azurefile"
      volumes:
        - name: azurefile
          persistentVolumeClaim:
            claimName: pvc-azurefile
  strategy:
    rollingUpdate:
      maxSurge: 0
      maxUnavailable: 1
    type: RollingUpdate
EOF
```
