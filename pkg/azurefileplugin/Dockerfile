# Copyright 2019 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

ARG ARCH=amd64

FROM registry.k8s.io/build-image/debian-base:bookworm-v1.0.6 AS base

FROM base AS builder

ARG ARCH

RUN apt update \
    && apt install -y curl \
    && curl -Lso /tmp/packages-microsoft-prod-22.04.deb https://packages.microsoft.com/config/ubuntu/22.04/packages-microsoft-prod.deb \
    && curl -Ls https://github.com/Azure/azure-storage-azcopy/releases/download/v10.30.1/azcopy_linux_${ARCH}_10.30.1.tar.gz \
        | tar xvzf - --strip-components=1 -C /usr/local/bin/ --wildcards "*/azcopy"

FROM base

ARG ARCH
ARG binary=./_output/${ARCH}/azurefileplugin

COPY ${binary} /azurefileplugin
COPY --from=builder --chown=root:root /usr/local/bin/azcopy /usr/local/bin/azcopy

RUN apt update && apt upgrade -y && apt-mark unhold libcap2 && clean-install ca-certificates cifs-utils util-linux e2fsprogs mount udev xfsprogs nfs-common netbase

COPY ./pkg/azurefile-proxy/*.sh /azurefile-proxy/
COPY ./pkg/azurefile-proxy/azurefile-proxy.service /azurefile-proxy/
COPY ./_output/${ARCH}/azurefile-proxy /azurefile-proxy/

RUN chmod +x /azurefile-proxy/*.sh && \
  chmod +x /azurefile-proxy/azurefile-proxy.service && \
  chmod +x /azurefile-proxy/azurefile-proxy

COPY --from=builder --chown=root:root /tmp/packages-microsoft-prod-22.04.deb /azurefile-proxy/packages-microsoft-prod-22.04.deb
RUN dpkg -i /azurefile-proxy/packages-microsoft-prod-22.04.deb && apt update && apt install -y azfilesauth && rm -f /azurefile-proxy/packages-microsoft-prod-22.04.deb

LABEL maintainers="andyzhangx"
LABEL description="AzureFile CSI Driver"

ENTRYPOINT ["/azurefileplugin"]
