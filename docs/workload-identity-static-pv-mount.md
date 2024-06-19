# workload identity support on static provisioning
 - supported from v1.30.1 (from AKS 1.29 with `tokenRequests` field support in `CSIDriver`)

The feature is intended for SMB mount and is not supported for NFS mount since NFS mount does not need credentials. Each pod has its own standalone SMB mount, but if multiple pods are present on a single node, it may lead to performance problems.

## Prerequisites
### 1. Create a cluster with oidc-issuer enabled and get the credential
Following the [documentation](https://learn.microsoft.com/en-us/azure/aks/use-oidc-issuer#create-an-aks-cluster-with-oidc-issuer) to create an AKS cluster with the `--enable-oidc-issuer` parameter and get the AKS credentials. And export following environment variables:
```
export RESOURCE_GROUP=<your resource group name>
export CLUSTER_NAME=<your cluster name>
export REGION=<your region>
```

### 2. Bring your own storage account and Azure file share
Following the [documentation](https://learn.microsoft.com/en-us/azure/storage/files/storage-how-to-use-files-portal?tabs=azure-cli) to create a new storage account and fileshare or use your own. And export following environment variables:
```
export STORAGE_RESOURCE_GROUP=<your storage account resource group>
export ACCOUNT=<your storage account name>
export SHARE=<your fileshare name>
```

### 3. Create managed identity and role assignment
```
export UAMI=<your managed identity name>
az identity create --name $UAMI --resource-group $RESOURCE_GROUP

export USER_ASSIGNED_CLIENT_ID="$(az identity show -g $RESOURCE_GROUP --name $UAMI --query 'clientId' -o tsv)"
export IDENTITY_TENANT=$(az aks show --name $CLUSTER_NAME --resource-group $RESOURCE_GROUP --query identity.tenantId -o tsv)
export ACCOUNT_SCOPE=$(az storage account show --name $ACCOUNT --query id -o tsv)

# please retry if you meet `Cannot find user or service principal in graph database` error, it may take a while for the identity to propagate
az role assignment create --role "Storage Account Contributor" --assignee $USER_ASSIGNED_CLIENT_ID --scope $ACCOUNT_SCOPE
```

### 4. Create service account on AKS
```
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
```
export FEDERATED_IDENTITY_NAME=<your federated identity name>
export AKS_OIDC_ISSUER="$(az aks show --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME --query "oidcIssuerProfile.issuerUrl" -o tsv)"

az identity federated-credential create --name $FEDERATED_IDENTITY_NAME \
--identity-name $UAMI \
--resource-group $RESOURCE_GROUP \
--issuer $AKS_OIDC_ISSUER \
--subject system:serviceaccount:${SERVICE_ACCOUNT_NAMESPACE}:${SERVICE_ACCOUNT_NAME}
```

## Pod with ephemeral inline volume
```
cat <<EOF | kubectl apply -f -
kind: Pod
apiVersion: v1
metadata:
  name: nginx-azurefile-inline-volume
spec:
  serviceAccountName: $SERVICE_ACCOUNT_NAME #required, Pod does not use this service account has no permission to mount the volume
  nodeSelector:
    "kubernetes.io/os": linux
  containers:
    - image: mcr.microsoft.com/oss/nginx/nginx:1.19.5
      name: nginx-azurefile
      command:
        - "/bin/bash"
        - "-c"
        - set -euo pipefail; while true; do echo $(date) >> /mnt/azurefile/outfile; sleep 1; done
      volumeMounts:
        - name: persistent-storage
          mountPath: "/mnt/azurefile"
  volumes:
    - name: persistent-storage
      csi:
        driver: file.csi.azure.com
        volumeAttributes:
          storageaccount: $ACCOUNT # required
          shareName: $SHARE  # required
          clientID: $USER_ASSIGNED_CLIENT_ID # required
          resourcegroup: $STORAGE_RESOURCE_GROUP # optional, specified when the storage account is not under AKS node resource group(which is prefixed with "MC_")
          # tenantID: $IDENTITY_TENANT  # optional, only specified when workload identity and AKS cluster are in different tenant
          # subscriptionid: $SUBSCRIPTION # optional, only specified when workload identity and AKS cluster are in different subscription
EOF
```
