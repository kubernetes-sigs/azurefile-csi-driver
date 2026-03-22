# Workload Identity Support for Azure File CSI Driver

- **Supported from:** v1.30.1 (AKS 1.29+ with `tokenRequests` field support in `CSIDriver`)

## Limitations

- This feature is **not supported for NFS mounts** since NFS does not require credentials.
- By default, this feature retrieves the storage account key using federated identity credentials. Alternatively, you can mount with a **workload identity token only** (Preview) by:
  - Setting `mountWithWorkloadIdentityToken: "true"` in the `parameters` of the StorageClass or PersistentVolume
  - Granting the `Storage File Data SMB MI Admin` role (instead of `Storage Account Contributor`) to the managed identity

  > [!NOTE]
  > Mounting with workload identity token only is supported from **v1.35.0**.

## Prerequisites

### 1. Create an AKS cluster with OIDC issuer enabled

Refer to the [documentation](https://learn.microsoft.com/en-us/azure/aks/use-oidc-issuer#create-an-aks-cluster-with-oidc-issuer) for instructions on creating a new AKS cluster with the `--enable-oidc-issuer` parameter and retrieving the AKS credentials.

Export the following environment variables:

```bash
export RESOURCE_GROUP=<your resource group name>
export CLUSTER_NAME=<your cluster name>
export REGION=<your region>
```

### 2. Bring your own storage account

Refer to the [documentation](https://learn.microsoft.com/en-us/azure/storage/files/storage-how-to-use-files-portal?tabs=azure-cli) for instructions on creating a new storage account and file share, or use your existing ones.

Export the following environment variables:

```bash
export STORAGE_RESOURCE_GROUP=<your storage account resource group>
export ACCOUNT=<your storage account name>
export SHARE=<your file share name>  # optional
```

### 3. Create a managed identity and grant the required role

> [!TIP]
> You can leverage the built-in user-assigned managed identity bound to the AKS agent node pool (named [`<AKS Cluster Name>-agentpool`](https://docs.microsoft.com/en-us/azure/aks/use-managed-identity#summary-of-managed-identities)) in the node resource group.

Create a managed identity and retrieve its properties:

```bash
export UAMI=<your managed identity name>
az identity create --name "$UAMI" --resource-group "$RESOURCE_GROUP"

export USER_ASSIGNED_CLIENT_ID="$(az identity show -g "$RESOURCE_GROUP" --name "$UAMI" --query 'clientId' -o tsv)"

export IDENTITY_TENANT="$(az aks show --name "$CLUSTER_NAME" --resource-group "$RESOURCE_GROUP" --query identity.tenantId -o tsv)"

export ACCOUNT_SCOPE="$(az storage account show --name "$ACCOUNT" --query id -o tsv)"
```

Then choose **one** of the following role assignment options:

**Option A:** Grant `Storage Account Contributor` to retrieve account key (default)

```bash
az role assignment create --role "Storage Account Contributor"--assignee "$USER_ASSIGNED_CLIENT_ID" --scope "$ACCOUNT_SCOPE"
```

**Option B:** Grant `Storage File Data SMB MI Admin` for mounting with workload identity token only (no account key)

```bash
az role assignment create --role "Storage File Data SMB MI Admin" --assignee "$USER_ASSIGNED_CLIENT_ID" --scope "$ACCOUNT_SCOPE"
```

### 4. Create a service account on AKS

```bash
export SERVICE_ACCOUNT_NAME=<your service account name>
export SERVICE_ACCOUNT_NAMESPACE=<your service account namespace>

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${SERVICE_ACCOUNT_NAME}
  namespace: ${SERVICE_ACCOUNT_NAMESPACE}
EOF
```

### 5. Create the federated identity credential

Create the federated identity credential between the managed identity, service account issuer, and subject:

```bash
export FEDERATED_IDENTITY_NAME=<your federated identity name>
export AKS_OIDC_ISSUER="$(az aks show \
  --resource-group "$RESOURCE_GROUP" --name "$CLUSTER_NAME" \
  --query "oidcIssuerProfile.issuerUrl" -o tsv)"

az identity federated-credential create \
  --name "$FEDERATED_IDENTITY_NAME" \
  --identity-name "$UAMI" \
  --resource-group "$RESOURCE_GROUP" \
  --issuer "$AKS_OIDC_ISSUER" \
  --subject "system:serviceaccount:${SERVICE_ACCOUNT_NAMESPACE}:${SERVICE_ACCOUNT_NAME}"
```

## Option 1: Dynamic Provisioning with StorageClass

Ensure that the CSI driver control plane identity is assigned the **`Storage Account Contributor`** role for the storage account.

> [!NOTE]
> - If the storage account is created by the driver, grant the `Storage Account Contributor` role on the **resource group** where the storage account is located.
> - AKS cluster control plane identity is assigned the `Storage Account Contributor` role on the node resource group by default.

```bash
cat <<EOF | kubectl apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: azurefile-csi-wi
provisioner: file.csi.azure.com
parameters:
  storageaccount: $ACCOUNT                 # required
  clientID: $USER_ASSIGNED_CLIENT_ID       # required
  resourcegroup: $STORAGE_RESOURCE_GROUP   # optional, only needed when storage account is outside the node resource group (MC_*)
  mountWithWorkloadIdentityToken: "true"   # only supported from CSI driver v1.35.0
reclaimPolicy: Delete
volumeBindingMode: Immediate
allowVolumeExpansion: true
mountOptions:
  - dir_mode=0777    # modify for enhanced security
  - file_mode=0777
  - mfsymlinks
  - cache=strict     # https://linux.die.net/man/8/mount.cifs
  - nosharesock      # reduce probability of reconnect race
  - actimeo=30       # reduce latency for metadata-heavy workloads
  - nobrl            # disable byte range lock requests to the server
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
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      serviceAccountName: $SERVICE_ACCOUNT_NAME  # required for workload identity
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
  volumeClaimTemplates:
    - metadata:
        name: persistent-storage
      spec:
        storageClassName: azurefile-csi-wi
        accessModes: ["ReadWriteMany"]
        resources:
          requests:
            storage: 100Gi
EOF
```

## Option 2: Static Provisioning with PersistentVolume

> [!IMPORTANT]
> If you are using your own storage account with `mountWithWorkloadIdentityToken: "true"` in the PV parameters, ensure that the **SMBOauth** property is enabled:
>
> ```bash
> az storage account update \
>   --name <account-name> \
>   --resource-group <resource-group-name> \
>   --enable-smb-oauth true
> ```

```bash
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
    - dir_mode=0777    # modify for enhanced security
    - file_mode=0777
    - mfsymlinks
    - cache=strict     # https://linux.die.net/man/8/mount.cifs
    - nosharesock      # reduce probability of reconnect race
    - actimeo=30       # reduce latency for metadata-heavy workloads
    - nobrl            # disable byte range lock requests to the server
  csi:
    driver: file.csi.azure.com
    # make sure volumeHandle is unique for every identical share in the cluster
    volumeHandle: "{resource-group-name}#{account-name}#{file-share-name}"
    volumeAttributes:
      storageaccount: $ACCOUNT                # required
      shareName: $SHARE                       # required
      clientID: $USER_ASSIGNED_CLIENT_ID      # required
      resourcegroup: $STORAGE_RESOURCE_GROUP  # optional, only needed when storage account is outside the node resource group (MC_*)
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
  name: deployment-azurefile
  namespace: ${SERVICE_ACCOUNT_NAMESPACE}
  labels:
    app: nginx
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
      serviceAccountName: $SERVICE_ACCOUNT_NAME  # required for workload identity
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

 - Once the example pod is running successfully, you will see the following output:

```sh
# kubectl exec -it statefulset-azurefile-0 -- mount | grep cifs
//accountname.file.core.windows.net/pvc-d62dcee0-9102-417f-9869-5f0e885f7f10 on /mnt/azurefile type cifs (rw,relatime,vers=3.1.1,sec=krb5,cruid=0,cache=strict,upcall_target=mount,username=root,uid=0,noforceuid,gid=0,noforcegid,addr=52.239.239.104,file_mode=0777,dir_mode=0777,soft,persistenthandles,nounix,serverino,mapposix,nobrl,mfsymlinks,rsize=1048576,wsize=1048576,bsize=1048576,retrans=1,echo_interval=60,nosharesock,actimeo=30,closetimeo=1)
```
