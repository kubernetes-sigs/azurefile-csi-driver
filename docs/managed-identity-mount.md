# Mount Azure SMB File Share with Managed Identity

- **Feature status:** GA
- **Supported from:** CSI driver v1.34.0 on Linux nodes

This article demonstrates how to mount an SMB file share using user-assigned managed identity authentication, without relying on account key authentication.

> [!IMPORTANT]
> **Managed identity mount is not supported for inline (ephemeral) CSI volumes.** Requests with `mountWithManagedIdentity: "true"` on a `csi:` inline volume in a Pod spec are rejected by the node driver; use a StorageClass (dynamic) or PersistentVolume (static) so the identity and `clientID` are controlled by the cluster admin, not by tenant Pods. Inline volumes remain supported with workload identity (`mountWithWorkloadIdentityToken: "true"`) or secret-based authentication.

> [!NOTE]
> By default, you can leverage the built-in user-assigned managed identity (kubelet identity) bound to the AKS agent node pool (with the naming convention [`<AKS Cluster Name>-agentpool`](https://docs.microsoft.com/en-us/azure/aks/use-managed-identity#summary-of-managed-identities)).

> [!IMPORTANT]
> If you have created your own managed identity, make sure it is associated with the agent node pool. Use the following command to bind the managed identity to the VMSS node pool:
>
> ```bash
> az vmss identity assign \
>   --name <vmss-name> \
>   --resource-group <resource-group-name> \
>   --identities <managed-identity-resource-id>
> ```

## Prerequisites

### 1. Grant the required role to the managed identity

Make sure the managed identity is granted the **`Storage File Data SMB MI Admin`** role on the storage account.

> [!NOTE]
> If the storage account is created by the driver (dynamic provisioning), you need to grant the `Storage File Data SMB MI Admin` role on the **resource group** where the storage account is located.
> 
> If you encounter permission issues when running the az role assignment create command, you can assign the necessary role through the Azure portal's `Access Control (IAM)` page.

```bash
# Get the principal ID of the managed identity
mid="$(az identity list -g "$resourcegroup" --query "[?name == 'managedIdentityName'].principalId" -o tsv)"

# Get the storage account resource ID
said="$(az storage account list -g "$resourcegroup" --query "[?name == '$storageaccountname'].id" -o tsv)"

# Assign the role
az role assignment create --assignee-object-id "$mid" --role "Storage File Data SMB MI Admin" --scope "$said"
```

### 2. Retrieve the client ID of the managed identity

> [!TIP]
> Skip this step if you plan to use the kubelet identity. The CSI driver defaults to the kubelet identity when the `clientID` parameter is not provided in the StorageClass or PersistentVolume.

```bash
clientID=$(az identity list -g "$resourcegroup" --query "[?name == '$identityname'].clientId" -o tsv)
```

## Dynamic Provisioning

Ensure that the CSI driver control plane identity is assigned the **`Storage Account Contributor`** role for the storage account.

> [!NOTE]
> - If the storage account is created by the driver, grant the `Storage Account Contributor` role on the **resource group** where the storage account is located.
> - AKS cluster control plane identity is assigned the `Storage Account Contributor` role on the node resource group by default.

### Step 1: Create a StorageClass

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: azurefile-csi
provisioner: file.csi.azure.com
parameters:
  resourceGroup: EXISTING_RESOURCE_GROUP_NAME   # optional, defaults to node resource group
  storageAccount: EXISTING_STORAGE_ACCOUNT_NAME # optional, a new account will be created if not provided
  mountWithManagedIdentity: "true"
  clientID: "xxxxx-xxxx-xxx-xxx-xxxxxxx"        # optional, defaults to kubelet identity
reclaimPolicy: Delete
volumeBindingMode: Immediate
allowVolumeExpansion: true
mountOptions:
  - dir_mode=0777    # modify for enhanced security
  - file_mode=0777
  - uid=0
  - gid=0
  - mfsymlinks
  - cache=strict     # https://linux.die.net/man/8/mount.cifs
  - nosharesock      # reduce probability of reconnect race
  - actimeo=30       # reduce latency for metadata-heavy workloads
  - nobrl            # disable sending byte range lock requests to the server
```

### Step 2: Create a StatefulSet with volume mount

```bash
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/statefulset.yaml
```

## Static Provisioning

> [!IMPORTANT]
> If you are using your own storage account, ensure that the **SMBOauth** property is enabled:
>
> ```bash
> az storage account update \
>   --name <account-name> \
>   --resource-group <resource-group-name> \
>   --enable-smb-oauth true
> ```

### Step 1: Create a PersistentVolume

```yaml
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
    - dir_mode=0777    # modify for enhanced security
    - file_mode=0777
    - uid=0
    - gid=0
    - mfsymlinks
    - cache=strict     # https://linux.die.net/man/8/mount.cifs
    - nosharesock      # reduce probability of reconnect race
    - actimeo=30       # reduce latency for metadata-heavy workloads
    - nobrl            # disable sending byte range lock requests to the server
  csi:
    driver: file.csi.azure.com
    # make sure volumeHandle is unique for every identical share in the cluster
    volumeHandle: "{resource-group-name}#{account-name}#{file-share-name}"
    volumeAttributes:
      resourceGroup: EXISTING_RESOURCE_GROUP_NAME   # optional, defaults to node resource group
      storageAccount: EXISTING_STORAGE_ACCOUNT_NAME # ensure SMBOauth is enabled on this account
      shareName: EXISTING_FILE_SHARE_NAME
      mountWithManagedIdentity: "true"
      clientID: "xxxxx-xxxx-xxx-xxx-xxxxxxx"        # optional, defaults to kubelet identity
```

### Step 2: Create a PVC and Deployment with volume mount

```bash
kubectl create -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/deployment.yaml
```

 - Once the example pod is running successfully, you will see the following output:

```sh
# kubectl exec -it statefulset-azurefile-0 -- mount | grep cifs
//accountname.file.core.windows.net/pvc-1bfefee3-652e-4fd3-b32d-30044f28ef0e on /mnt/azurefile type cifs (rw,relatime,vers=3.1.1,sec=krb5,cruid=0,cache=strict,upcall_target=mount,username=c56002c7-a601-44d1-b5d0-9bbc593edb12,uid=0,noforceuid,gid=0,noforcegid,addr=52.239.239.104,file_mode=0777,dir_mode=0777,soft,persistenthandles,nounix,serverino,mapposix,nobrl,mfsymlinks,rsize=1048576,wsize=1048576,bsize=1048576,retrans=1,echo_interval=60,nosharesock,actimeo=30,closetimeo=1)
```

## Troubleshooting

### Error: `Error calling AzAuthenticatorLib: -1` / `Error getting Kerberos service ticket`

If you see the following error in the CSI driver node pod logs:

```
Error calling AzAuthenticatorLib: -1
Error getting Kerberos service ticket, check /var/log/syslog for more information.
```

Verify the following:

1. **The managed identity has the correct role assignment.** Ensure the managed identity is assigned the **`Storage File Data SMB MI Admin`** role on the storage account (or the resource group for dynamic provisioning). Other roles such as `Storage File Data SMB Share Contributor` or `Storage File Data SMB Share Elevated Contributor` are **not sufficient** for managed identity mount.

2. **The SMBOauth property is enabled on the storage account.** Without this, the storage account does not support Kerberos ticket acquisition for managed identity authentication.

   ```bash
   az storage account show --name <storageAccountName> --resource-group <resourceGroup> \
     --query "smbOAuthSettings.isSmbOAuthEnabled"
   ```

3. **The managed identity is attached to the VMSS and propagated to the failing node instance.** `az vmss identity show` only reports the VMSS *model* configuration — it does not tell you whether the identity has actually propagated to a specific instance. The authoritative per-node check is to call IMDS from the node itself (see step (a) below).

   ```bash
   az vmss identity show -g <nodeResourceGroup> -n <vmssName> \
     --query 'userAssignedIdentities'
   ```

#### Collecting the real error from the failing node

`Error calling AzAuthenticatorLib: -1` is a generic error code. The real root cause is one of:

- IMDS cannot return a token for the configured `clientID` (identity not propagated to the instance, or wrong `clientID`).
- Token is acquired but the Kerberos ticket exchange with the storage endpoint is blocked (network / storage firewall / private endpoint misconfiguration).
- Clock skew > 5 minutes on the node (Kerberos is time-sensitive).

To find out which one it is, run the following on the `csi-azurefile-node` pod that is scheduled on the failing node:

```bash
# 1. Find the csi-azurefile-node pod on the failing node
kubectl get pods -n kube-system -l app=csi-azurefile-node -o wide | grep <failingNodeName>

# 2. Exec into the azurefile container
kubectl exec -it <csi-azurefile-node-pod> -n kube-system -c azurefile -- sh
```

Then inside the container:

```sh
CLIENT_ID=<the clientID from the PV / StorageClass>
STG=<storageAccountName>

# (a) Verify IMDS can issue a token for this clientID from the node.
# The driver requests the storage OAuth resource https://storage.azure.com/
# (see pkg/azurefile/azurefile.go), so use the same resource here.
# Do NOT print or paste the raw access_token anywhere - it is a bearer
# credential. The one-liner below only reports whether a token was
# returned (with the token redacted), plus the expiry.
curl -sS -H "Metadata:true" \
  "http://169.254.169.254/metadata/identity/oauth2/token?api-version=2018-02-01&client_id=${CLIENT_ID}&resource=https://storage.azure.com/" \
  | python3 -c "import sys, json; d=json.load(sys.stdin); print({k: ('<redacted>' if k=='access_token' else v) for k,v in d.items()})"
# Expect a dict with access_token=<redacted> and expires_on set.
# If you see {"error":"invalid_request","error_description":"Identity not found"},
# the managed identity is not attached to this VMSS instance yet.

# (b) Run azfilesauthmanager manually with verbose output to see the real error
azfilesauthmanager set https://${STG}.file.core.windows.net \
  --imds-client-id ${CLIENT_ID} -v 2>&1 | tee /tmp/azfauth.log

# (c) Kernel-side CIFS / Kerberos errors
dmesg -T 2>/dev/null | grep -iE "cifs|azauth|kerberos|krb|upcall" | tail -50
# If dmesg is empty from inside the container, run from the host via nsenter:
#   nsenter -t 1 -m -u -i -n -p tail -300 /var/log/syslog | grep -iE "azfileauth|azauth|kerberos|krb"
```

Interpretation:

| Output of (a) | Meaning |
|---|---|
| `{"error":"invalid_request",..."Identity not found"}` | The `clientID` is not attached to this VMSS instance. Re-run `az vmss identity assign` and make sure the update is applied to all instances. |
| Valid JSON with `access_token` | IMDS is fine — move on to (b). |

| Output of (b) / (c) | Meaning |
|---|---|
| `KRB5KDC_ERR_S_PRINCIPAL_UNKNOWN` / `no such service` | The storage account's Kerberos SPN was not found. Confirm the storage account name / endpoint is correct in the PV / StorageClass, and that Azure AD Kerberos / SMB OAuth is enabled on the account (`smbOAuthSettings.isSmbOAuthEnabled` = true). |
| `Clock skew too great` / `KRB_AP_ERR_SKEW` | Node clock is drifted more than 5 minutes. Fix NTP / `chronyd` on the node. |
| Connection timeout to `login.microsoftonline.com` or the storage endpoint | Network / firewall / private endpoint issue. Verify egress and any storage account firewall rules allow the node subnet. |
| `AADSTS700016` / `AADSTS7000215` | The managed identity is disabled or its service principal was deleted. Recreate the identity and re-assign it. |
