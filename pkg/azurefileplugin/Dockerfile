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

FROM registry.k8s.io/build-image/debian-base:bookworm-v1.0.3

ARG ARCH=amd64
ARG binary=./_output/${ARCH}/azurefileplugin
COPY ${binary} /azurefileplugin

RUN apt update && apt upgrade -y && apt-mark unhold libcap2 && clean-install ca-certificates cifs-utils util-linux e2fsprogs mount udev xfsprogs nfs-common netbase wget

# install azcopy
ARG azcopyURL=https://azcopyvnext.azureedge.net/releases/release-10.24.0-20240326/azcopy_linux_amd64_10.24.0.tar.gz
RUN if [ "$ARCH" == "arm64" ] ; then \
  azcopyURL=https://azcopyvnext.azureedge.net/releases/release-10.24.0-20240326/azcopy_linux_arm64_10.24.0.tar.gz; fi
RUN wget -O azcopy.tar.gz ${azcopyURL} && \
  tar xvzf azcopy.tar.gz -C . && rm azcopy.tar.gz && \
  mv ./azcopy_linux_$ARCH_*/azcopy /usr/local/bin/azcopy && \
  rm -rf ./azcopy_linux_$ARCH_*
RUN chmod +x /usr/local/bin/azcopy
RUN apt remove wget -y

LABEL maintainers="andyzhangx"
LABEL description="AzureFile CSI Driver"

ENTRYPOINT ["/azurefileplugin"]
