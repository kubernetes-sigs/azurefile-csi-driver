#!/bin/bash

# Copyright 2020 The Kubernetes Authors.
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

set -euo pipefail

REPO_ROOT=$(realpath $(dirname ${BASH_SOURCE})/..)
COPYRIGHT_FILE="${REPO_ROOT}/hack/boilerplate/boilerplate.generatego.txt"

if ! type mockgen &> /dev/null; then
    echo "mockgen not exist, install it"
    go install github.com/golang/mock/mockgen@v1.6.0
fi

echo "Updating mocks for util.go"
mockgen -copyright_file=$COPYRIGHT_FILE -source=pkg/util/util.go -package=util -destination=pkg/util/util_mock.go
