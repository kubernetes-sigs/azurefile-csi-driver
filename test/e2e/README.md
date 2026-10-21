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
Compilation and Ginkgo dry-run discovery are not native mount execution.

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
