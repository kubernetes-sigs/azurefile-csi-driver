## End to End Test

## Run E2E tests Locally

### Authentication coverage

The authentication specs have the Ginkgo label `auth` and live in
`authentication*_test.go`. They are table-driven: `authenticationScenarios` in
`authentication_test.go` lists every supported combination of volume kind
(dynamic, pre-provisioned, CSI inline), dynamic `useDataPlaneAPI` (ARM, `true`,
`oauth`) and mount credential (node ListKeys, workload identity account key,
explicit or configured managed identity), and each row runs through an
existing tester. Spec names state the combination, for example
`Authentication and mounting should write and read on a pre-provisioned volume with explicit managed identity Kerberos`.

* **Route evidence.** Each spec uses a fresh account and single-role identity.
  The accounts require infrastructure encryption, so the driver never places
  other specs' volumes in them.
  Every positive pod checks `/proc/mounts` for the account, `sec=` and
  `username=` before writing and reading. Each non-dynamic, non-configured row
  also runs as a negative twin ("should fail to mount without the route's
  credential") with shared-key access disabled or no storage role, which must
  produce `FailedMount` and stay unstarted for a minute.
* **Other specs.** Explicit OAuth REST accounts mounted with managed identity
  Kerberos, with shared-key access on and off; key Secrets for single CSI
  operations (provision, stage, expand, clone, plus wrong-key twins); and two
  workload identity key principals on one account and node with a denied third.

* **Fixtures.** Identity specs need the CAPZ lane, OIDC setup, and permission
  to create accounts, shares, user-assigned identities, federated credentials
  and role assignments, and to attach identities to the selected worker. The
  clone spec reads the controller's SAS-generation log, so the controller needs
  verbosity 2 or more. The inline node-key row needs a lane started with
  `--allow-inline-volume-key-access-with-identity=true` and is skipped
  otherwise; the inline workload identity key row is skipped when it is on.
* **Limits.** The driver documentation supports one Kerberos identity per
  account per node, so multiple principals are covered only for account keys.

List the specs without running them:

```console
ARTIFACTS=./_artifacts go test -p 2 -run '^TestE2E$' ./test/e2e -args \
  -ginkgo.dry-run -ginkgo.label-filter=auth
```
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
   dropped for the Ginkgo CLI. Driver install and teardown run once; each
   process starts its own in-process driver and checks the OIDC issuer before
   its first workload identity spec.
 - Specs that change cluster-wide state are marked `Serial` and run on one
   process after all parallel specs: the smb/nfs driver restart cases.
   Pre-Provisioned, BYO key and CSI inline specs are also `Serial`: they call
   the in-process driver's `CreateVolume`, which could otherwise select another
   process's default account before it finishes provisioning. Other specs use
   their own accounts, identities, namespaces or claim names.

 - resize e2e test needs more environment setting, export following environment variables from `azure.json`
```console
export AZURE_TENANT_ID=
export AZURE_SUBSCRIPTION_ID=
export AZURE_CLIENT_ID=
export AZURE_CLIENT_SECRET=
export AZURE_RESOURCE_GROUP=
export AZURE_LOCATION=
```
