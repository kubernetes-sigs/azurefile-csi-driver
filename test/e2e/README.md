## End to End Test

## Run E2E tests Locally

### Authentication coverage

`[auth-mount-matrix]` selects the V1 authentication scenarios and the supported
entry/controller-API completion cases. The scenario identifiers are stable:
N01-N19 are V1, M01-M09 complete the entry/API matrix, and G01 is the explicit
workload-token/ARM counterpart. G01 is included in matrix completion rather
than declared again by later configuration/lifecycle coverage.

Identity fixtures currently require the CAPZ suite setup, an available worker
UAMI, successful OIDC/FIC initialization for workload identity, and permission
to create/assign/detach test identities. Configured-identity cases deliberately
omit `clientID`; the configured WI FIC uses the suite's default-namespace
ServiceAccount. Distinct explicit-identity cases remain separate.

M09 requires a dedicated lane already configured with
`--allow-inline-volume-key-access-with-identity=true`; the test does not change
the DaemonSet. Default inline WI regression cases require that option disabled.
Run these incompatible configurations in separate lanes and report skips.
Inline lane checks run before Azure account, share or identity provisioning.
Configured-WI cases share the suite ServiceAccount's namespace; only this
fixture's account and named/generated credential Secrets must be key-free.
Compilation and Ginkgo dry-run discovery are not native mount execution.

#### Additional authentication scenarios

`[auth-additional]` selects G02-G20 and G22-G25 (G21 was removed because key mounts leave no host credential state for a restart to exercise); G01 remains in `[auth-mount-matrix]`.
All original V1 and matrix-completion cases remain separate. These specs use
native Azure SDK account/share/identity fixtures and Kubernetes PVC, pod,
Secret, resize, clone and node-plugin operations. They do not change driver
implementation or install a separate authentication test harness.

| Cases | Native operation and evidence |
|---|---|
| G02-G04 | Static MI/WI-token/OAuth-token mounts using the canonical SMB hostname; actual CIFS `addr=` must equal the supplied private endpoint address, account public access must be disabled, an approved endpoint connection must exist, and remote data must survive remount. |
| G05-G07 | One combined spec applies the CSIDriver renewal configuration once and runs the three fixtures concurrently in separate pods/shares. Each keeps one long-running pod and unchanged volume/mount, observed unchanged republish, actual later-expiring credential replacement, helper handoff/refresh evidence, sustained I/O through the original JWT's expiry for G05/G06. G05/G06 inspect only their dedicated identity/account token file; G07 replaces a valid Azure access token in the same Secret and stops after the refreshed credential reaches the existing mount, because managed-identity storage tokens live about 24 hours. |
| G08 | Two unique SAs/FICs/client IDs, two shares on one account/worker; observe each WI ListKeys identity and concurrent I/O/remounts. Both SMB usernames legitimately equal the account name. |
| G09-G11 | **Principal-sharing investigations, not a supported isolation guarantee.** Each fresh principal receives the data role only on its own share, never a resource-group-wide data role. Concurrent positive I/O and peer-preserving remounts test substitution without a deliberately unauthorized user. |
| G12-G14 | Explicit existing account, actual ARM `allowSharedKeyAccess=false`, controller request without key credentials, explicit MI/WI-token/OAuth-token mount and persisted data. |
| G15-G17 | Shared-key-enabled controls for G12-G14; preserve `storageAccount`, omit tag discovery and generated key persistence. |
| G18 | Provisioner-only request Secret; verify sanitized controller delivery, absent node-stage ref/credentials, no generated mount-key Secret, actual node configured-identity key lookup and NTLMSSP I/O. |
| G19 | Node-stage-only request Secret; ARM provisioning input with no provisioner credentials, `storeAccountKey=false`, exact node-stage ref and observed key delivery to the node. |
| G20 | Separate provisioner/node-stage/controller-expand Secret objects; observe expand request credentials, change capacity through the resizer, check PVC status, PV and actual Azure share quota, and retain I/O/data in the original running pod. |
| G22-G24 | One combined spec prepares the MI/WI-token/OAuth-token fixtures concurrently on one worker, deletes every application pod, waits until **all mounts of each fixture share disappear**, replaces that worker's node plugin once, then freshly mounts each PV/share with intended authentication and persisted data, starting from the leftover node Kerberos cache and an empty driver cache. Assert successful post-restart staging; never force-unmount unrelated shares. `nosharesock` prevents a remount from sharing another mount's socket. |
| G25 | Supplied provisioner operation key forces same-account SAS cloning; assert sanitized supplied-key request, account-specific SAS-generation and source/destination copy logs, distinct shares, and source/destination marker data. Never print keys, tokens or SAS URLs. |

**Supported-contract and proof limits:** the current
[MI documentation](../../docs/managed-identity-mount.md#limitations) and
[WI documentation](../../docs/workload-identity-static-pv-mount.md#limitations)
state **one Kerberos identity per account per node**. G09-G11 deliberately test
the stronger requested isolation target and may fail against the current
server-keyed helper/cache contract. The Kerberos investigation cases have Ginkgo label
`auth-investigation` and require `AZUREFILE_MULTI_IDENTITY_KERBEROS=true` before
creating fixtures. Without that opt-in G09-G11 visibly skip. Supported G08
does not require it and remains included when that label is excluded. Do not report
discovery, skipped cases, separate token filenames, or broadly authorized
concurrent mounts as passing isolation coverage.

G05/G06 establish source-JWT expiry; G05-G07 establish real replacement and helper invocation
while preserving the active mount. They **do not establish expiry/destruction
of an existing kernel SMB session or its Kerberos service ticket**; successful
I/O alone cannot prove a surviving session needed renewed credentials. That
stronger expiry/re-authentication target remains a supported-observability
blocker, not something the specs manufacture by recreating the pod or
force-clearing shared host credentials.

For inline renewal, successful helper evidence must come after the replacement
token was observed; earlier unchanged-token republish success is insufficient.
Ordinary two-pod matrix cases establish data persistence, not a new SMB session
per pod. The restart and principal-sharing cases explicitly wait for full
unstaging when their scenario requires fresh authentication.

Sanitized component requests establish credential **delivery**, not secret
byte logging or a complete audit of Azure authorization calls. In particular,
G12-G17 do not claim zero ARM ListKeys calls: disabling shared-key data-plane
authorization does not disable ARM ListKeys, and an application's `clientID`
does not replace the controller credential. A stronger no-ListKeys claim
requires a dedicated controller credential with **no effective inherited
ListKeys permission**, or trustworthy Azure call auditing. None of these specs
assumes adding a narrow role removes an inherited broad role.

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
* G05-G07 and G22-G24 require `AZUREFILE_AUTH_DISRUPTIVE=true` on a dedicated
  isolated CAPZ cluster. G05-G07 additionally require driver verbosity **4 or
  higher**, permission to update `CSIDriver` renewal fields, pod exec/log access,
  and sufficient wall-clock budget (OAuth tokens can live over an hour).
  Renewal temporarily sets `requiresRepublish=true` and the token-exchange
  audience TTL to 600 seconds, preserves other audiences/fields, and restores
  the original values with conflict checks. If the cluster forbids those
  updates, the test fails rather than deleting/recreating the shared object.
  Restart tests delete only the selected node-plugin pod, not the DaemonSet.
* Account-policy, credential-delivery and clone evidence requires the standard
  `app=csi-azurefile-controller` / `app=csi-azurefile-node` pod labels,
  `azurefile` container, component verbosity at least **2**, and read access
  to their logs. Missing requested evidence fails; there is no fallback to
  treating a configured parameter as an observed event.
  OAuth REST rows require account-correlated OAuth-client construction during
  provisioning, not just the selector parameter or an eventual successful mount.

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

# Force a serial run, or change the parallel process count
make e2e-test E2E_PROCS=1
make e2e-test E2E_PROCS=8
```

 - On Linux CSI clusters, `make e2e-test` runs specs on 4 parallel Ginkgo
   processes by default, using the Ginkgo CLI version pinned in `go.mod`
   (installed under `_output/tools`). Windows, migration and in-tree runs stay
   serial. `GINKGO_FLAGS` keeps the `-ginkgo.` test-binary syntax; the prefix is
   dropped for the Ginkgo CLI. Driver install, workload identity setup and
   teardown run once; each process starts its own in-process driver.
 - Specs that change cluster-wide state are marked `Serial` and run on one
   process after all parallel specs: the smb/nfs driver restart cases, the
   suite workload identity token case, G05-G07 renewal and G22-G24 node plugin
   restart. G02-G04 share one private-endpoint account and run as an `Ordered`
   group. Other specs use their own accounts, identities, namespaces or claim
   names; configured-WI cases share the `default` namespace with per-case claim
   names.

 - resize e2e test needs more environment setting, export following environment variables from `azure.json`
```console
export AZURE_TENANT_ID=
export AZURE_SUBSCRIPTION_ID=
export AZURE_CLIENT_ID=
export AZURE_CLIENT_SECRET=
export AZURE_RESOURCE_GROUP=
export AZURE_LOCATION=
```
