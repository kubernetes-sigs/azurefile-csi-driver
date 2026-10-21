## End to End Test

## Run E2E tests Locally

### Authentication coverage

`[auth-mount-matrix]` selects the V1 authentication scenarios and the supported
entry/controller-API completion cases. The scenario identifiers are stable:
N01-N19 are V1 identifiers across releases. This release adds M01-M04 and M09;
WI-token and OAuth-token mounting cases require 1.35 and are not registered here.

Identity fixtures currently require the CAPZ suite setup, an available worker
UAMI, successful OIDC/FIC initialization for workload identity, and permission
to create/assign/detach test identities. Configured-identity cases deliberately
omit `clientID`. Distinct explicit-identity cases remain separate; configured
WI-token mounting requires 1.35 and is not declared in this release.

M09 requires a dedicated lane already configured with
`--allow-inline-volume-key-access-with-identity=true`; the test does not change
the DaemonSet. Default inline WI regression cases require that option disabled.
Run these incompatible configurations in separate lanes and report skips.
Inline lane checks run before Azure account, share or identity provisioning.
Configured-WI cases share the suite ServiceAccount's namespace; only this
fixture's account and named/generated credential Secrets must be key-free.
Compilation and Ginkgo dry-run discovery are not native mount execution.

#### Additional authentication scenarios

`[auth-additional]` selects exactly G02, G08, G09, G12, G15, G18-G20, G22, G25,
the G18/G19 wrong-key twins and the D01-D04 negative controls on this release
(16 specs). G01 and the other WI-token/OAuth-token mount
scenarios require 1.35 and are not declared here. The table below also
describes those 1.35-only counterparts for reference.
These specs follow the existing e2e pattern: each `It` builds StorageClass
parameters or volume attributes and `PodDetails`, then runs an existing tester
(`DynamicallyProvisionedCmdVolumeTest`, `PreProvisionedMultiplePods`,
`DynamicallyProvisionedInlineVolumeTest`, `DynamicallyProvisionedResizeVolumeTest`,
`DynamicallyProvisionedVolumeCloningTest`, `DynamicallyProvisionedInvalidMountOptions`)
or one of the small authentication testers in `testsuites/`. Azure accounts,
shares and identities are created with the Azure SDK; the driver is not changed.

Every positive pod checks `/proc/mounts` for the expected account
(`//<account>.file.<suffix>/`), `sec=` and `username=` options before writing
and reading data.

| Cases | Operation and evidence |
|---|---|
| G02 (G03-G04: 1.35 only) | Static MI (1.35: also WI-token/OAuth-token) mounts of the canonical SMB hostname; the CIFS `addr=` must equal the private endpoint address. |
| G05-G07 (1.35 only) | Separate Slow/Disruptive specs (`PreProvisionedCredentialRenewalTest`). Each applies the CSIDriver renewal configuration, keeps one pod and mount unchanged, and requires a later-expiring credential and a newer node Kerberos ticket for the server. G05/G06 keep I/O through the original JWT's expiry; G07 rotates the OAuth token Secret and stops when the refreshed credential reaches the mount, because managed-identity storage tokens live about 24 hours. |
| G08 | `PreProvisionedConcurrentMountsTest`: two principals with share-scoped roles on one account and node, each remounted in turn while the other keeps I/O; a third roleless principal must fail to mount. |
| G09 (G10-G11: 1.35 only) | **Principal-sharing investigations, not a supported isolation guarantee.** Same flow as G08 for MI (1.35: also WI-token/OAuth-token) without the denied pod. |
| G12 (G13-G14: 1.35 only) | Explicit existing account with ARM `allowSharedKeyAccess=false` and `useDataPlaneAPI=oauth`; MI (1.35: also WI-token/OAuth-token) mount. |
| G15 (G16-G17: 1.35 only) | Shared-key-enabled control for G12. |
| G18 | Provisioner-only key Secret with `useDataPlaneAPI=true`; a wrong-key twin must report `ProvisioningFailed`. |
| G19 | Node-stage-only key Secret; a wrong-key twin must report `FailedMount`. |
| G20 | Provisioner, node-stage and controller-expand Secrets; `DynamicallyProvisionedResizeVolumeTest` checks the PVC, PV and Azure share quota. |
| G22 (G23-G24: 1.35 only) | Disruptive spec: mount, delete the pods, restart the selected node plugin, then mount again with the same authentication and read the data. |
| G25 | Provisioner operation key Secret; `DynamicallyProvisionedVolumeCloningTest` plus the controller's account-specific SAS-generation log. Never print keys, tokens or SAS URLs. |
| D01-D04 (D05-D06: 1.35 only) | Negative twins of M01, N03, N04 and N06 (`PreProvisionedMountFailureTest`). Each removes only the route's credential (shared-key access disabled for M01, no storage role otherwise) and must produce `FailedMount`, so the positive case cannot pass through another route. |

**Supported-contract and proof limits:** the current
[MI documentation](../../docs/managed-identity-mount.md#limitations) and
[1.35 WI documentation](https://github.com/kubernetes-sigs/azurefile-csi-driver/blob/v1.35.11/docs/workload-identity-static-pv-mount.md#limitations)
state **one Kerberos identity per account per node**. G09 (1.35: also G10-G11) tests the stronger
isolation target and may fail against the current contract. They have the
Ginkgo label `auth-investigation` and skip unless
`AZUREFILE_MULTI_IDENTITY_KERBEROS=true`. G08 does not require it.

On 1.35, G05-G07 establish credential replacement and node Kerberos cache refresh while
the mount stays active. They **do not establish expiry of an existing kernel
SMB session or its Kerberos service ticket**.

Route evidence is behavioral: fresh accounts, single-role identities, CIFS
mount options and the negative twins. The only driver log read is G25's SAS
generation. G15 does not prove the OAuth client is preferred while shared-key
access is allowed, and G12/G15 do not claim zero ARM ListKeys calls.

##### Required lanes and permissions

* Additional fixtures retain the existing **CAPZ/Linux/CSI/non-migration**
  boundary; they do not enable cloud mutations in arbitrary AKS test clusters.
  Permit creation/deletion of test accounts, shares, UAMIs, FICs and owned role
  assignments. MI fixtures also attach/detach a new UAMI only on the selected
  worker, preserving its original identities and checking the VM ETag. A
  stale ETag or in-progress update is re-read and retried, so concurrent MI
  fixtures on one worker do not overwrite each other's identities.
* For G02-G04, supply `AZUREFILE_AUTH_PRIVATE_ACCOUNT` (in the suite resource
  group) and `AZUREFILE_AUTH_PRIVATE_IP` (a private IPv4 address). The account
  must already have SMB OAuth enabled, **publicNetworkAccess=Disabled**, and an
  approved file private endpoint with canonical private DNS reachable from the
  worker. The harness creates/deletes only its unique share and identities;
  it never modifies/deletes the shared endpoint, DNS zone, subnet or account.
  Missing fixture variables produce a visible capability skip; a supplied but
  incorrect/public fixture fails. This does not validate dynamically emitted
  private-link aliases on 1.34.
* G22 requires `AZUREFILE_AUTH_DISRUPTIVE=true` on a dedicated
  isolated CAPZ cluster. The 1.35-only G05-G07 additionally require
  permission to update `CSIDriver` renewal fields, node-plugin pod exec access,
  and sufficient wall-clock budget (OAuth tokens can live over an hour).
  Renewal temporarily sets `requiresRepublish=true` and the token-exchange
  audience TTL to 600 seconds, preserves other audiences/fields, and restores
  the original values with conflict checks. If the cluster forbids those
  updates, the test fails rather than deleting/recreating the shared object.
  Restart tests delete only the selected node-plugin pod, not the DaemonSet.
* G25 SAS evidence requires the standard `app=csi-azurefile-controller` pod
  label, `azurefile` container, verbosity at least **2**, and log read access.

For example, discover all additional declarations **without native execution**:

```console
ARTIFACTS=./_artifacts go test -p 2 -run '^TestE2E$' ./test/e2e -args \
  -ginkgo.dry-run -ginkgo.focus='\[auth-additional\]'
```

Native execution must be explicitly selected in a provisioned lane. Report
case outcomes and capability skips separately; do not publish an all-G passing
claim while the above isolation/expiry/auditing limitations remain.

##### Release adaptation

The ordinary/MI counterpart set for 1.34 is
G02, G08, G09, G12, G15, G18-G20, G22, G25 (10 declarations; G01 is unavailable).
Omit G03-G07, G10-G11, G13-G14, G16-G17 and G23-G24 from 1.34 declarations.
The applicable 1.34 negative twins are D01-D04 and the G18/G19 wrong-key twins.
The renewal testsuite and its CSIDriver configuration are token-only and need
not be backported to 1.34. All G cases are applicable declarations on
1.35/master, subject to the explicit capability and contract limits above.
Keep release-specific V1/M declarations and G01 unchanged when adapting the
shared fixture helpers; share-scoped roles and per-operation Secret handling
are common to both releases.

### Prerequisite
 - Make sure a kubernetes cluster(with version >= 1.13) is set up and kubeconfig is under `$HOME/.kube/config`
 - Copy out `/etc/kubernetes/azure.json` under one agent node to local machine
 > For AKS cluster, need to modify `resourceGroup` to the node resource group name inside `/etc/kubernetes/azure.json`

### How to run E2E tests

```console
# Using CSI Driver
make e2e-test

# Using in-tree volume plugin
export AZURE_STORAGE_DRIVER="kubernetes.io/azure-file"
make e2e-test

# Run in a Windows cluster
export TEST_WINDOWS="true"
make e2e-test

# Run migration test
export TEST_MIGRATION="true"
export AZURE_STORAGE_DRIVER="kubernetes.io/azure-file"
make e2e-test
```

 - resize e2e test needs more environment setting, export following environment variables from `azure.json`
```console
export AZURE_TENANT_ID=
export AZURE_SUBSCRIPTION_ID=
export AZURE_CLIENT_ID=
export AZURE_CLIENT_SECRET=
export AZURE_RESOURCE_GROUP=
export AZURE_LOCATION=
```
