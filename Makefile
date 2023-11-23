# Copyright 2017 The Kubernetes Authors.
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

PKG = sigs.k8s.io/azurefile-csi-driver
GIT_COMMIT ?= $(shell git rev-parse HEAD)
REGISTRY ?= andyzhangx
REGISTRY_NAME ?= $(shell echo $(REGISTRY) | sed "s/.azurecr.io//g")
IMAGE_NAME ?= azurefile-csi
IMAGE_VERSION ?= v1.29.2
# Use a custom version for E2E tests if we are testing in CI
ifdef CI
ifndef PUBLISH
override IMAGE_VERSION := e2e-$(GIT_COMMIT)
endif
endif
CSI_IMAGE_TAG ?= $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_VERSION)
CSI_IMAGE_TAG_LATEST = $(REGISTRY)/$(IMAGE_NAME):latest
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS ?= "-X ${PKG}/pkg/azurefile.driverVersion=${IMAGE_VERSION} -X ${PKG}/pkg/azurefile.gitCommit=${GIT_COMMIT} -X ${PKG}/pkg/azurefile.buildDate=${BUILD_DATE} -s -w -extldflags '-static'"
E2E_HELM_OPTIONS ?= --set image.azurefile.repository=$(REGISTRY)/$(IMAGE_NAME) --set image.azurefile.tag=$(IMAGE_VERSION) --set linux.dnsPolicy=ClusterFirstWithHostNet --set driver.userAgentSuffix="e2e-test"
E2E_HELM_OPTIONS += ${EXTRA_HELM_OPTIONS}
ifdef KUBERNETES_VERSION # disable kubelet-registration-probe on capz cluster testing
E2E_HELM_OPTIONS += --set linux.enableRegistrationProbe=false --set windows.enableRegistrationProbe=false
endif
ifdef EXTERNAL_E2E_TEST_NFS
E2E_HELM_OPTIONS += --set feature.enableVolumeMountGroup=false --set feature.fsGroupPolicy=File
endif
GINKGO_FLAGS = -ginkgo.v
GO111MODULE = on
GOPATH ?= $(shell go env GOPATH)
GOBIN ?= $(GOPATH)/bin
DOCKER_CLI_EXPERIMENTAL = enabled
export GOPATH GOBIN GO111MODULE DOCKER_CLI_EXPERIMENTAL

# Generate all combination of all OS, ARCH, and OSVERSIONS for iteration
ALL_OS = linux windows
ALL_ARCH.linux = amd64 arm64
ALL_OS_ARCH.linux = $(foreach arch, ${ALL_ARCH.linux}, linux-$(arch))
ALL_ARCH.windows = amd64
ALL_OSVERSIONS.windows := 1809 ltsc2022
ALL_OS_ARCH.windows = $(foreach arch, $(ALL_ARCH.windows), $(foreach osversion, ${ALL_OSVERSIONS.windows}, windows-${osversion}-${arch}))
ALL_OS_ARCH = $(foreach os, $(ALL_OS), ${ALL_OS_ARCH.${os}})

# The current context of image building
# The architecture of the image
ARCH ?= amd64
# OS Version for the Windows images: 1809 ltsc2022
OSVERSION ?= 1809
# Output type of docker buildx build
OUTPUT_TYPE ?= registry

.EXPORT_ALL_VARIABLES:

.PHONY: all
all: azurefile

.PHONY: update
update:
	hack/update-dependencies.sh
	hack/verify-update.sh

.PHONY: verify
verify: unit-test
	hack/verify-all.sh

.PHONY: unit-test
unit-test:
	go test -v -race ./pkg/... ./test/utils/credentials

.PHONY: sanity-test
sanity-test: azurefile
	go test -v -timeout=10m ./test/sanity

.PHONY: integration-test
integration-test: azurefile
	go test -v -timeout=10m ./test/integration

.PHONY: e2e-test
e2e-test:
	if [ ! -z "$(EXTERNAL_E2E_TEST_SMB)" ] || [ ! -z "$(EXTERNAL_E2E_TEST_NFS)" ]; then \
		bash ./test/external-e2e/run.sh;\
	else \
		bash ./hack/parse-prow-creds.sh;\
		go test -v -timeout=0 ./test/e2e ${GINKGO_FLAGS};\
	fi

.PHONY: e2e-bootstrap
e2e-bootstrap: install-helm
ifdef WINDOWS_USE_HOST_PROCESS_CONTAINERS
	(docker pull $(CSI_IMAGE_TAG) && docker pull $(CSI_IMAGE_TAG)-windows-hp) || make container-all push-manifest
else
	docker pull $(CSI_IMAGE_TAG) || make container-all push-manifest
endif
ifdef TEST_WINDOWS
	helm install azurefile-csi-driver charts/latest/azurefile-csi-driver --namespace kube-system --wait --timeout=15m -v=5 --debug \
		${E2E_HELM_OPTIONS} \
		--set windows.enabled=true \
		--set windows.useHostProcessContainers=${WINDOWS_USE_HOST_PROCESS_CONTAINERS} \
		--set linux.enabled=false \
		--set driver.azureGoSDKLogLevel=INFO \
		--set controller.replicas=1 \
		--set controller.logLevel=6 \
		--set node.logLevel=10
else
	helm install azurefile-csi-driver charts/latest/azurefile-csi-driver --namespace kube-system --wait --timeout=15m -v=5 --debug \
		${E2E_HELM_OPTIONS} \
		--set snapshot.enabled=true
endif

.PHONY: install-helm
install-helm:
	curl https://raw.githubusercontent.com/helm/helm/master/scripts/get-helm-3 | bash

.PHONY: e2e-teardown
e2e-teardown:
	helm delete azurefile-csi-driver --namespace kube-system

.PHONY: azurefile
azurefile:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -a -ldflags ${LDFLAGS} -mod vendor -o _output/${ARCH}/azurefileplugin ./pkg/azurefileplugin

.PHONY: azurefile-windows
azurefile-windows:
	CGO_ENABLED=0 GOOS=windows go build -a -ldflags ${LDFLAGS} -mod vendor -o _output/${ARCH}/azurefileplugin.exe ./pkg/azurefileplugin

.PHONY: azurefile-darwin
azurefile-darwin:
	CGO_ENABLED=0 GOOS=darwin go build -a -ldflags ${LDFLAGS} -mod vendor -o _output/${ARCH}/azurefileplugin ./pkg/azurefileplugin

.PHONY: container
container: azurefile
	docker build --no-cache -t $(CSI_IMAGE_TAG) --output=type=docker -f ./pkg/azurefileplugin/Dockerfile .

.PHONY: container-linux
container-linux:
	docker buildx build --pull --output=type=$(OUTPUT_TYPE) --platform="linux/$(ARCH)" \
		--provenance=false --sbom=false \
		-t $(CSI_IMAGE_TAG)-linux-$(ARCH) --build-arg ARCH=${ARCH} -f ./pkg/azurefileplugin/Dockerfile .

.PHONY: container-windows
container-windows:
	docker buildx build --pull --output=type=$(OUTPUT_TYPE) --platform="windows/$(ARCH)" \
		-t $(CSI_IMAGE_TAG)-windows-$(OSVERSION)-$(ARCH) --build-arg OSVERSION=$(OSVERSION) \
		--provenance=false --sbom=false \
		--build-arg ARCH=${ARCH} -f ./pkg/azurefileplugin/Windows.Dockerfile .
# workaround: only build hostprocess image once
ifdef WINDOWS_USE_HOST_PROCESS_CONTAINERS
ifeq ($(OSVERSION),ltsc2022)
	$(MAKE) container-windows-hostprocess
endif
endif

# Set --provenance=false to not generate the provenance (which is what causes the multi-platform index to be generated, even for a single platform).
.PHONY: container-windows-hostprocess
container-windows-hostprocess:
	docker buildx build --pull --output=type=$(OUTPUT_TYPE) --platform="windows/$(ARCH)" --provenance=false --sbom=false \
		-t $(CSI_IMAGE_TAG)-windows-hp -f ./pkg/azurefileplugin/WindowsHostProcess.Dockerfile .

.PHONY: container-windows-hostprocess-latest
container-windows-hostprocess-latest:
	docker buildx build --pull --output=type=$(OUTPUT_TYPE) --platform="windows/$(ARCH)" --provenance=false --sbom=false \
		-t $(CSI_IMAGE_TAG_LATEST)-windows-hp -f ./pkg/azurefileplugin/WindowsHostProcess.Dockerfile .

.PHONY: container-all
container-all: azurefile-windows
	docker buildx rm container-builder || true
	docker buildx create --use --name=container-builder
	# enable qemu for arm64 build
	# https://github.com/docker/buildx/issues/464#issuecomment-741507760
	docker run --privileged --rm tonistiigi/binfmt --uninstall qemu-aarch64
	docker run --rm --privileged tonistiigi/binfmt --install all
	for arch in $(ALL_ARCH.linux); do \
		ARCH=$${arch} $(MAKE) azurefile; \
		ARCH=$${arch} $(MAKE) container-linux; \
	done
	for osversion in $(ALL_OSVERSIONS.windows); do \
		OSVERSION=$${osversion} $(MAKE) container-windows; \
	done

.PHONY: push-manifest
push-manifest:
	docker manifest create --amend $(CSI_IMAGE_TAG) $(foreach osarch, $(ALL_OS_ARCH), $(CSI_IMAGE_TAG)-${osarch})
	set -x; \
	for arch in $(ALL_ARCH.windows); do \
		for osversion in $(ALL_OSVERSIONS.windows); do \
			BASEIMAGE=mcr.microsoft.com/windows/nanoserver:$${osversion}; \
			full_version=`docker manifest inspect $${BASEIMAGE} | jq -r '.manifests[0].platform["os.version"]'`; \
			docker manifest annotate --os windows --arch $${arch} --os-version $${full_version} $(CSI_IMAGE_TAG) $(CSI_IMAGE_TAG)-windows-$${osversion}-$${arch}; \
		done; \
	done
	docker manifest push --purge $(CSI_IMAGE_TAG)
	docker manifest inspect $(CSI_IMAGE_TAG)
ifdef PUBLISH
	docker manifest create --amend $(CSI_IMAGE_TAG_LATEST) $(foreach osarch, $(ALL_OS_ARCH), $(CSI_IMAGE_TAG)-${osarch})
	set -x; \
	for arch in $(ALL_ARCH.windows); do \
		for osversion in $(ALL_OSVERSIONS.windows); do \
			BASEIMAGE=mcr.microsoft.com/windows/nanoserver:$${osversion}; \
			full_version=`docker manifest inspect $${BASEIMAGE} | jq -r '.manifests[0].platform["os.version"]'`; \
			docker manifest annotate --os windows --arch $${arch} --os-version $${full_version} $(CSI_IMAGE_TAG_LATEST) $(CSI_IMAGE_TAG)-windows-$${osversion}-$${arch}; \
		done; \
	done
	docker manifest inspect $(CSI_IMAGE_TAG_LATEST)
	docker manifest create --amend $(CSI_IMAGE_TAG_LATEST)-windows-hp $(CSI_IMAGE_TAG_LATEST)-windows-hp
	docker manifest inspect $(CSI_IMAGE_TAG_LATEST)-windows-hp
endif

.PHONY: push-latest
push-latest:
ifdef CI
	docker manifest push --purge $(CSI_IMAGE_TAG_LATEST)
	docker manifest push --purge $(CSI_IMAGE_TAG_LATEST)-windows-hp
else
	docker push $(CSI_IMAGE_TAG_LATEST)
	docker push $(CSI_IMAGE_TAG_LATEST)-windows-hp
endif

.PHONY: clean
clean:
	go clean -r -x
	-rm -rf _output

.PHONY: create-metrics-svc
create-metrics-svc:
	kubectl create -f deploy/example/metrics/csi-azurefile-controller-svc.yaml

.PHONY: install-smb-provisioner
install-smb-provisioner:
	kubectl delete secret smbcreds --ignore-not-found
	kubectl create secret generic smbcreds --from-literal azurestorageaccountname=USERNAME --from-literal azurestorageaccountkey="PASSWORD"
ifdef TEST_WINDOWS
	kubectl apply -f deploy/example/smb-provisioner/smb-server-lb.yaml
else
	kubectl apply -f deploy/example/smb-provisioner/smb-server.yaml
endif
