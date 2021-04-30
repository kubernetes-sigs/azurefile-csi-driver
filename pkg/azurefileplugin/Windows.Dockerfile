ARG OSVERSION
ARG ARCH=amd64
FROM --platform=linux/${ARCH} gcr.io/k8s-staging-e2e-test-images/windows-servercore-cache:1.0-linux-${ARCH}-${OSVERSION} as core

FROM mcr.microsoft.com/windows/nanoserver:${OSVERSION}
LABEL description="CSI Azure file plugin"

ARG ARCH=amd64
COPY ./_output/${ARCH}/azurefileplugin.exe /azurefileplugin.exe
COPY --from=core /Windows/System32/netapi32.dll /Windows/System32/netapi32.dll
USER ContainerAdministrator
ENTRYPOINT ["/azurefileplugin.exe"]
