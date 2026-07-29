# azurefile-csi-driver helm chart repository

This branch hosts the Helm chart repository for azurefile-csi-driver via
GitHub Pages. Published automatically by the
`Publish Helm Chart to GitHub Pages` workflow.

To use:

    helm repo add azurefile-csi-driver https://kubernetes-sigs.github.io/azurefile-csi-driver
    helm repo update azurefile-csi-driver
    helm install azurefile-csi-driver azurefile-csi-driver/azurefile-csi-driver \
      --namespace kube-system --version <x.y.z>
