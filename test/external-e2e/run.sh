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

set -xe

PROJECT_ROOT=$(git rev-parse --show-toplevel)
DRIVER="test"

setup_e2e_binaries() {
    # download k8s external e2e binary for kubernetes
    curl -sL https://dl.k8s.io/release/v1.26.0/kubernetes-test-linux-amd64.tar.gz --output e2e-tests.tar.gz
    tar -xvf e2e-tests.tar.gz && rm e2e-tests.tar.gz

    # test on alternative driver name
    export EXTRA_HELM_OPTIONS=" --set driver.name=$DRIVER.csi.azure.com --set controller.name=csi-$DRIVER-controller --set linux.dsName=csi-$DRIVER-node --set windows.dsName=csi-$DRIVER-node-win"
    sed -i "s/file.csi.azure.com/$DRIVER.csi.azure.com/g" deploy/example/storageclass-azurefile-csi.yaml
    sed -i "s/file.csi.azure.com/$DRIVER.csi.azure.com/g" deploy/example/storageclass-azurefile-nfs.yaml
    # run e2e test on standard storage class since premium file share only supports volume size >= 100GiB
    sed -i "s/Premium/Standard/g" deploy/example/storageclass-azurefile-csi.yaml
    make e2e-bootstrap
    sed -i "s/csi-azurefile-controller/csi-$DRIVER-controller/g" deploy/example/metrics/csi-azurefile-controller-svc.yaml
    make create-metrics-svc
}

print_logs() {
    bash ./hack/verify-examples.sh linux
    echo "print out driver logs ..."
    bash ./test/utils/azurefile_log.sh $DRIVER
}

setup_e2e_binaries
trap print_logs EXIT

mkdir -p /tmp/csi

if [ ! -z ${EXTERNAL_E2E_TEST_SMB} ]; then
	echo "begin to run SMB protocol tests ...."
	cp deploy/example/storageclass-azurefile-csi.yaml /tmp/csi/storageclass.yaml
	ginkgo -p -v --fail-fast --flake-attempts 2 -focus="External.Storage.*$DRIVER.csi.azure.com" \
		-skip='\[Disruptive\]|volume contents ownership changed|should provision storage with any volume data source|should mount multiple PV pointing to the same storage on the same node' kubernetes/test/bin/e2e.test  -- \
		-storage.testdriver=$PROJECT_ROOT/test/external-e2e/testdriver-smb.yaml \
		--kubeconfig=$KUBECONFIG
fi

if [ ! -z ${EXTERNAL_E2E_TEST_NFS} ]; then
	echo "begin to run NFS protocol tests ...."
	cp deploy/example/storageclass-azurefile-nfs.yaml /tmp/csi/storageclass.yaml
	ginkgo -p -v --fail-fast --flake-attempts 2 -focus="External.Storage.*$DRIVER.csi.azure.com" \
		-skip='\[Disruptive\]|should provision storage with any volume data source|should mount multiple PV pointing to the same storage on the same node|pod created with an initial fsgroup, volume contents ownership changed via chgrp in first pod, new pod with same fsgroup applied to the volume contents' kubernetes/test/bin/e2e.test  -- \
		-storage.testdriver=$PROJECT_ROOT/test/external-e2e/testdriver-nfs.yaml \
		--kubeconfig=$KUBECONFIG
fi
