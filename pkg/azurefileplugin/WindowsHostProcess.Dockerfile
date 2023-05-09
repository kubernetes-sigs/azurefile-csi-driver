FROM mcr.microsoft.com/oss/kubernetes/windows-host-process-containers-base-image:v1.0.0
LABEL description="CSI Azure file plugin"

ARG ARCH=amd64
ARG binary=./_output/${ARCH}/azurefileplugin.exe
COPY ${binary} /azurefileplugin.exe
ENV PATH="C:\Windows\system32;C:\Windows;C:\WINDOWS\System32\WindowsPowerShell\v1.0\;"
USER ContainerAdministrator
ENTRYPOINT ["/azurefileplugin.exe"]
