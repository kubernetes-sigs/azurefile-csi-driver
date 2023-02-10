ARG ARCH=amd64
FROM --platform=linux/${ARCH} gcr.io/k8s-staging-e2e-test-images/windows-servercore-cache:1.0-linux-${ARCH}-1809 as core

FROM mcr.microsoft.com/oss/kubernetes/windows-host-process-containers-base-image:v1.0.0
LABEL description="CSI Azure file plugin"

ARG ARCH=amd64
ARG binary=./_output/${ARCH}/azurefileplugin.exe
COPY ${binary} /azurefileplugin.exe
COPY --from=core /Windows/System32/netapi32.dll /Windows/System32/netapi32.dll
ENV PATH="C:\Windows\system32;C:\Windows;C:\WINDOWS\System32\WindowsPowerShell\v1.0\;"
USER ContainerAdministrator
ENTRYPOINT ["/azurefileplugin.exe"]
