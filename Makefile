# Copyright (c) 2021 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

NAME              := virtual-garden
REPO_ROOT         := $(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))
HACK_DIR          := $(REPO_ROOT)/hack
VERSION           := $(shell cat "$(REPO_ROOT)/VERSION")
EFFECTIVE_VERSION := $(VERSION)-$(shell git rev-parse HEAD)
LD_FLAGS          := "-w $(shell $(REPO_ROOT)/vendor/github.com/gardener/gardener/hack/get-build-ld-flags.sh k8s.io/component-base $(REPO_ROOT)/VERSION $(NAME))"
OPERATION         := "RECONCILE"

REGISTRY                                 := eu.gcr.io/gardener-project/development
VIRTUAL_GARDEN_DEPLOYER_IMAGE_REPOSITORY := $(REGISTRY)/images/virtual-garden

#########################################
# Rules for local development scenarios #
#########################################

.PHONY: start
start:
	@LD_FLAGS=$(LD_FLAGS) \
	./hack/local-development/start.sh $(OPERATION)

#################################################################
# Rules related to binary build, Docker image build and release #
#################################################################

.PHONY: install
install:
	@LD_FLAGS=$(LD_FLAGS) \
	$(REPO_ROOT)/vendor/github.com/gardener/gardener/hack/install.sh ./...

.PHONY: docker-images
docker-images:
	@echo "Building docker images for version $(EFFECTIVE_VERSION)"
	@docker build --build-arg EFFECTIVE_VERSION=$(EFFECTIVE_VERSION) -t $(VIRTUAL_GARDEN_DEPLOYER_IMAGE_REPOSITORY):$(EFFECTIVE_VERSION) -f Dockerfile .

.PHONY: docker-push
docker-push:
	@echo "Pushing docker images for version $(EFFECTIVE_VERSION) to registry $(REGISTRY)"
	@if ! docker images $(VIRTUAL_GARDEN_DEPLOYER_IMAGE_REPOSITORY) | awk '{ print $$2 }' | grep -q -F $(EFFECTIVE_VERSION); then echo "$(VIRTUAL_GARDEN_DEPLOYER_IMAGE_REPOSITORY) version $(EFFECTIVE_VERSION) is not yet built. Please run 'make docker-images'"; false; fi
	@docker push $(VIRTUAL_GARDEN_DEPLOYER_IMAGE_REPOSITORY):$(EFFECTIVE_VERSION)

.PHONY: docker-all
docker-all: docker-images docker-push

.PHONY: cnudie
cnudie:
	@EFFECTIVE_VERSION=$(EFFECTIVE_VERSION) ./hack/generate-cd.sh

.PHONY: push
push: docker-images docker-push cnudie

.PHONY: create-installation
create-installation:
	@EFFECTIVE_VERSION=$(EFFECTIVE_VERSION) ./hack/create-installation.sh

#####################################################################
# Rules for verification, formatting, linting, testing and cleaning #
#####################################################################

.PHONY: install-requirements
install-requirements:
	@go install -mod=vendor $(REPO_ROOT)/vendor/github.com/onsi/ginkgo/ginkgo
	@go install -mod=vendor github.com/golang/mock/mockgen
	@go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
	@$(REPO_ROOT)/hack/install-requirements.sh

.PHONY: revendor
revendor:
	@GO111MODULE=on go mod vendor
	@GO111MODULE=on go mod tidy
	@chmod +x $(REPO_ROOT)/vendor/github.com/gardener/gardener/hack/*
	@$(REPO_ROOT)/hack/update-github-templates.sh

.PHONY: clean
clean:
	@$(REPO_ROOT)/vendor/github.com/gardener/gardener/hack/clean.sh ./cmd/... ./pkg/... ./test/...

.PHONY: check
check:
	@$(REPO_ROOT)/vendor/github.com/gardener/gardener/hack/check.sh --golangci-lint-config=./.golangci.yaml ./cmd/... ./pkg/... ./test/...

.PHONY: format
format:
	@$(REPO_ROOT)/vendor/github.com/gardener/gardener/hack/format.sh ./cmd ./pkg ./test

.PHONY: setup-testenv
setup-testenv:
	@$(REPO_ROOT)/hack/setup-testenv.sh

.PHONY: test
test:
	@$(REPO_ROOT)/vendor/github.com/gardener/gardener/hack/test.sh ./cmd/... ./pkg/...

.PHONY: test-e2e
test-e2e:
	ginkgo -v $(REPO_ROOT)/test/e2e

.PHONY: test-cov
test-cov:
	@$(REPO_ROOT)/vendor/github.com/gardener/gardener/hack/test-cover.sh -r ./cmd/... ./pkg/...

.PHONY: test-clean
test-clean:
	@$(REPO_ROOT)/vendor/github.com/gardener/gardener/hack/test-cover-clean.sh

.PHONY: verify
verify: check format test

.PHONY: verify-extended
verify-extended: install-requirements check format test-cov test-clean
