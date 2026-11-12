## End to End Test

## Run E2E tests Locally
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
   dropped for the Ginkgo CLI. Driver install and teardown run once;
   each process starts its own in-process driver.
 - Specs that change cluster-wide state are marked `Serial` and run on one
   process after all parallel specs: the smb/nfs driver restart cases.
   Pre-Provisioned, BYO key and CSI inline specs are also `Serial`: they call
   the in-process driver's `CreateVolume`, which could otherwise select another
   process's default account before it finishes provisioning. Other specs use
   their own accounts, namespaces or claim names.

 - resize e2e test needs more environment setting, export following environment variables from `azure.json`
```console
export AZURE_TENANT_ID=
export AZURE_SUBSCRIPTION_ID=
export AZURE_CLIENT_ID=
export AZURE_CLIENT_SECRET=
export AZURE_RESOURCE_GROUP=
export AZURE_LOCATION=
```
