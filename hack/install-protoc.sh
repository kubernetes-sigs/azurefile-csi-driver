#!/bin/bash

# Copyright 2021 The Kubernetes Authors.
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

PROTOCOL_BUF_VERSION=3.15.1
PROTOC_GEN_GO_GRPC_VERSION=v1.1.0
PROTOC_GEN_GO_VERSION=v1.25.0
wget https://github.com/protocolbuffers/protobuf/releases/download/v$PROTOCOL_BUF_VERSION/protoc-$PROTOCOL_BUF_VERSION-linux-x86_64.zip
wget https://github.com/grpc/grpc-go/releases/download/cmd/protoc-gen-go-grpc/$PROTOC_GEN_GO_GRPC_VERSION/protoc-gen-go-grpc.$PROTOC_GEN_GO_GRPC_VERSION.linux.amd64.tar.gz
wget wget https://github.com/protocolbuffers/protobuf-go/releases/download/$PROTOC_GEN_GO_VERSION/protoc-gen-go.$PROTOC_GEN_GO_VERSION.linux.amd64.tar.gz
sudo unzip -o protoc-$PROTOCOL_BUF_VERSION-linux-x86_64.zip -d /usr/local bin/protoc
sudo unzip -o protoc-$PROTOCOL_BUF_VERSION-linux-x86_64.zip -d /usr/local 'include/*'
sudo tar -C /usr/local/bin -xvf protoc-gen-go-grpc.$PROTOC_GEN_GO_GRPC_VERSION.linux.amd64.tar.gz
sudo tar -C /usr/local/bin -xvf protoc-gen-go.$PROTOC_GEN_GO_VERSION.linux.amd64.tar.gz
sudo chmod +x /usr/local/bin/protoc-gen-go
sudo chmod +x /usr/local/bin/protoc-gen-go-grpc
