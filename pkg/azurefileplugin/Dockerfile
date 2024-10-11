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

FROM registry.k8s.io/build-image/debian-base:bookworm-v1.0.4 AS base

FROM base AS builder

ARG ARCH

RUN apt update \
    && apt install -y curl \
    && curl -Ls https://azcopyvnext.azureedge.net/releases/release-10.26.0-20240731/azcopy_linux_${ARCH}_10.26.0.tar.gz \
        | tar xvzf - --strip-components=1 -C /usr/local/bin/ --wildcards "*/azcopy"

FROM base

ARG ARCH
ARG binary=./_output/${ARCH}/azurefileplugin

COPY ${binary} /azurefileplugin
COPY --from=builder --chown=root:root /usr/local/bin/azcopy /usr/local/bin/azcopy

# packages that are only needed by aznfs: procps conntrack iptables bind9-host iproute2 bash netcat sysvinit-utils kmod net-tools
RUN apt update && apt upgrade -y && apt-mark unhold libcap2 && clean-install ca-certificates cifs-utils util-linux e2fsprogs mount udev xfsprogs nfs-common netbase procps conntrack iptables bind9-host iproute2 bash netcat-traditional sysvinit-utils kmod net-tools

# install aznfs
ARG aznfsVer=0.1.349
RUN if [ "$ARCH" = "amd64" ] ; then \
  wget -O aznfs.tar.gz https://github.com/Azure/AZNFS-mount/releases/download/${aznfsVer}/aznfs-${aznfsVer}-1.x86_64.tar.gz; \
else \
  wget -O aznfs.tar.gz https://github.com/Azure/AZNFS-mount/releases/download/${aznfsVer}/aznfs-${aznfsVer}-1.arm64.tar.gz;fi
RUN tar xvzf aznfs.tar.gz -C / --keep-directory-symlink && rm aznfs.tar.gz

# install azcopy
ARG azcopyURL=https://azcopyvnext.azureedge.net/releases/release-10.25.1-20240612/azcopy_linux_amd64_10.25.1.tar.gz
RUN if [ "$ARCH" == "arm64" ] ; then \
  azcopyURL=https://azcopyvnext.azureedge.net/releases/release-10.25.1-20240612/azcopy_linux_arm64_10.25.1.tar.gz; fi
RUN wget -O azcopy.tar.gz ${azcopyURL} && \
  tar xvzf azcopy.tar.gz -C . && rm azcopy.tar.gz && \
  mv ./azcopy_linux_$ARCH_*/azcopy /usr/local/bin/azcopy && \
  rm -rf ./azcopy_linux_$ARCH_*
RUN chmod +x /usr/local/bin/azcopy
RUN apt remove wget -y

LABEL maintainers="andyzhangx"
LABEL description="AzureFile CSI Driver"

ENTRYPOINT ["/azurefileplugin"]
