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

FROM registry.k8s.io/build-image/debian-base:bookworm-v1.0.5 AS base

FROM base AS builder

ARG ARCH

RUN apt update \
    && apt install -y curl \
    && curl -Ls https://azcopyvnext-awgzd8g7aagqhzhe.b02.azurefd.net/releases/release-10.29.1-20250515/azcopy_linux_${ARCH}_10.29.1.tar.gz \
        | tar xvzf - --strip-components=1 -C /usr/local/bin/ --wildcards "*/azcopy"

FROM base

ARG ARCH
ARG binary=./_output/${ARCH}/azurefileplugin

COPY ${binary} /azurefileplugin
COPY --from=builder --chown=root:root /usr/local/bin/azcopy /usr/local/bin/azcopy

RUN apt update && apt upgrade -y && apt-mark unhold libcap2 && clean-install ca-certificates cifs-utils util-linux e2fsprogs mount udev xfsprogs nfs-common netbase

COPY ./pkg/azurefile-proxy/init.sh /azurefile-proxy/
COPY ./pkg/azurefile-proxy/install-proxy.sh /azurefile-proxy/
COPY ./pkg/azurefile-proxy/azurefile-proxy.service /azurefile-proxy/
COPY ./_output/${ARCH}/azurefile-proxy /azurefile-proxy/

RUN chmod +x /azurefile-proxy/init.sh && \
  chmod +x /azurefile-proxy/install-proxy.sh && \
  chmod +x /azurefile-proxy/azurefile-proxy.service && \
  chmod +x /azurefile-proxy/azurefile-proxy

LABEL maintainers="andyzhangx"
LABEL description="AzureFile CSI Driver"

ENTRYPOINT ["/azurefileplugin"]
