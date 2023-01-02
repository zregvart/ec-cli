MAKEFLAGS+=-j --no-print-directory
VERSION:=$$(git log -1 --format='%H')
# a list of "dist/ec_{platform}_{arch}" that we support
ALL_SUPPORTED_OS_ARCH:=$(shell go tool dist list -json|jq -r '.[] | select((.FirstClass == true or .GOARCH == "ppc64le") and .GOARCH != "386") | "dist/ec_\(.GOOS)_\(.GOARCH)"')
# a list of image_* targets that we do not support
UNSUPPORTED_OS_ARCH_IMG:=image_windows_amd64 image_darwin_amd64 image_darwin_arm64 image_linux_arm
# a list of image_* targets that we do support generated from
# ALL_SUPPORTED_OS_ARCH by replacing "dist/ec_" with "image_"
ALL_SUPPORTED_IMG_OS_ARCH:=$(filter-out $(UNSUPPORTED_OS_ARCH_IMG),$(subst dist/ec_,image_,$(ALL_SUPPORTED_OS_ARCH)))
SHELL=bash
ROOT_DIR:=$(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
COPY:="Red Hat, Inc."

##@ Information targets

.PHONY: help
help: ## Display this help.
	@awk 'function ww(s) {\
		if (length(s) < 59) {\
			return s;\
		}\
		else {\
			r="";\
			l="";\
			split(s, arr, " ");\
			for (w in arr) {\
				if (length(l " " arr[w]) > 59) {\
					r=r l "\n                     ";\
					l="";\
				}\
				l=l " " arr[w];\
			}\
			r=r l;\
			return r;\
		}\
	} BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9%/_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", "make " $$1, ww($$2) } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development targets

.PHONY: $(ALL_SUPPORTED_OS_ARCH)
$(ALL_SUPPORTED_OS_ARCH): ## Build binaries for specific platform/architecture, e.g. make dist/ec_linux_amd64
	@GOOS=$(word 2,$(subst _, ,$(notdir $@))); \
	GOARCH=$(word 3,$(subst _, ,$(notdir $@))); \
	GOOS=$${GOOS} GOARCH=$${GOARCH} go build -trimpath -ldflags="-s -w -X github.com/hacbs-contract/ec-cli/cmd.Version=$(VERSION)" -o dist/ec_$${GOOS}_$${GOARCH}; \
	sha256sum -b dist/ec_$${GOOS}_$${GOARCH} > dist/ec_$${GOOS}_$${GOARCH}.sha256

.PHONY: dist
dist: $(ALL_SUPPORTED_OS_ARCH) ## Build binaries for all supported operating systems and architectures

.PHONY: build
build: dist/ec_$(shell go env GOOS)_$(shell go env GOARCH) ## Build the ec binary for the current platform
	@ln -sf ec_$(shell go env GOOS)_$(shell go env GOARCH) dist/ec

.PHONY: reference-docs
reference-docs: ## Generate reference documentation input YAML files
	@rm -rf dist/reference
	@go run internal/documentation/documentation.go -yaml dist/reference

.PHONY: test
test: ## Run unit tests
	@go test -race -covermode=atomic -coverprofile=coverage-unit.out -timeout 500ms -tags=unit ./...
	@go test -race -covermode=atomic -coverprofile=coverage-integration.out -timeout 15s -tags=integration ./...
# Given the nature of generative tests the test timeout is increased from 500ms
# to 30s to accommodate many samples being generated and test cases being run.
	@go test -race -covermode=atomic -coverprofile=coverage-generative.out -timeout 30s -tags=generative ./...

.ONESHELL:
.SHELLFLAGS=-e -c
.PHONY: acceptance
acceptance: ## Run acceptance tests
	@ACCEPTANCE_WORKDIR="$$(mktemp -d)"
	@function cleanup() {
	  rm -rf "$${ACCEPTANCE_WORKDIR}"
	}
	@trap cleanup EXIT
	@cp -R . "$${ACCEPTANCE_WORKDIR}"
	@cd "$${ACCEPTANCE_WORKDIR}"
	@go run acceptance/coverage/coverage.go .
	@$(MAKE) build
	@export COVERAGE_FILEPATH="$${ACCEPTANCE_WORKDIR}"
	@export COVERAGE_FILENAME="-acceptance"
	@cd acceptance && go test ./...
	@go run -modfile "$${ACCEPTANCE_WORKDIR}/tools/go.mod" github.com/wadey/gocovmerge "$${ACCEPTANCE_WORKDIR}"/coverage-acceptance*.out > "$(ROOT_DIR)/coverage-acceptance.out"

LICENSE_IGNORE=-ignore 'dist/reference/*.yaml'
LINT_TO_GITHUB_ANNOTATIONS='map(map(.)[])[][] as $$d | $$d.posn | split(":") as $$posn | "::warning file=\($$posn[0]),line=\($$posn[1]),col=\($$posn[2])::\($$d.message)"'
.PHONY: lint
lint: ## Run linter
# addlicense doesn't give us a nice explanation so we prefix it with one
	@go run -modfile tools/go.mod github.com/google/addlicense -c $(COPY) -s -check $(LICENSE_IGNORE) . | sed 's/^/Missing license header in: /g'
# piping to sed above looses the exit code, luckily addlicense is fast so we invoke it for the second time to exit 1 in case of issues
	@go run -modfile tools/go.mod github.com/google/addlicense -c $(COPY) -s -check $(LICENSE_IGNORE) . >/dev/null 2>&1
	@go run -modfile tools/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run --sort-results $(if $(GITHUB_ACTIONS), --out-format=github-actions --timeout=5m0s)
# We don't fail on the internal (error handling) linter, we just report the
# issues for now.
# TODO: resolve the error handling issues and enable the linter failure
	@go run -modfile tools/go.mod ./internal/lint $(if $(GITHUB_ACTIONS), -json) $$(go list ./... | grep -v '/acceptance/') $(if $(GITHUB_ACTIONS), | jq -r $(LINT_TO_GITHUB_ANNOTATIONS))

.PHONY: lint-fix
lint-fix: ## Fix linting issues automagically
	@go run -modfile tools/go.mod github.com/google/addlicense -c $(COPY) -s -ignore 'dist/reference/*.yaml' .
	@go run -modfile tools/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run --fix
# We don't apply the fixes from the internal (error handling) linter.
# TODO: fix the outstanding error handling lint issues and enable the fixer
#	@go run -modfile tools/go.mod ./internal/lint -fix $$(go list ./... | grep -v '/acceptance/')
	@go run -modfile tools/go.mod github.com/daixiang0/gci write -s standard -s default -s "prefix(github.com/hacbs-contract/ec-cli)" .

.PHONY: ci
ci: test lint-fix acceptance ## Run the usual required CI tasks

.PHONY: clean
clean: ## Delete build output
	@rm dist/*

IMAGE_TAG ?= latest
IMAGE_REPO ?= quay.io/hacbs-contract/ec-cli
.PHONY: build-image
build-image: image_$(shell go env GOOS)_$(shell go env GOARCH) ## Build container image with ec-cli

.PHONY: push-image
push-image: push_image_$(shell go env GOOS)_$(shell go env GOARCH) ## Push ec-cli container image to default location

.PHONY: build-snapshot-image
build-snapshot-image: push-image ## Build the ec-cli image and tag it with "snapshot"
	@podman tag $(IMAGE_REPO):$(IMAGE_TAG) $(IMAGE_REPO):snapshot

.PHONY: push-snapshot-image
push-snapshot-image: build-snapshot-image ## Push the ec-cli image with the "snapshot" tag
	@podman push $(PODMAN_OPTS) $(IMAGE_REPO):snapshot

.PHONY: $(ALL_SUPPORTED_IMG_OS_ARCH)
# Ref: https://www.gnu.org/software/make/manual/make.html#Secondary-Expansion
.SECONDEXPANSION:
# Targets are in the form of "image_{platform}_{arch}", we set
# TARGETOS={platform}, and TARGETARCH={arch}. This target depends on the
# "dist/ec_{platform}_{arch}" target
$(ALL_SUPPORTED_IMG_OS_ARCH): TARGETOS=$(word 2,$(subst _, ,$@))
$(ALL_SUPPORTED_IMG_OS_ARCH): TARGETARCH=$(word 3,$(subst _, ,$@))
$(ALL_SUPPORTED_IMG_OS_ARCH): $$(subst image_,dist/ec_,$$@)
	@podman build -t $(IMAGE_REPO):$(IMAGE_TAG)-$(TARGETOS)-$(TARGETARCH) -f Dockerfile --platform $(TARGETOS)/$(TARGETARCH)

.PHONY: $(subst image_,push_image_,$(ALL_SUPPORTED_IMG_OS_ARCH))
# Ref: https://www.gnu.org/software/make/manual/make.html#Secondary-Expansion
.SECONDEXPANSION:
# Targets are in the form of "push_image_{platform}_{arch}", we set
# TARGETOS={platform}, and TARGETARCH={arch}. This target depends on the
# "image_{platform}_{arch}" target
$(subst image_,push_image_,$(ALL_SUPPORTED_IMG_OS_ARCH)): TARGETOS=$(word 3,$(subst _, ,$@))
$(subst image_,push_image_,$(ALL_SUPPORTED_IMG_OS_ARCH)): TARGETARCH=$(word 4,$(subst _, ,$@))
$(subst image_,push_image_,$(ALL_SUPPORTED_IMG_OS_ARCH)): image_$$(TARGETOS)_$$(TARGETARCH)
	@podman push $(PODMAN_OPTS) $(IMAGE_REPO):$(IMAGE_TAG)-$(TARGETOS)-$(TARGETARCH)

.PHONY: dist-image
# Depends on targets in the form of "image_{platform}_{arch}"
dist-image: $(ALL_SUPPORTED_IMG_OS_ARCH) ## Build images for all supported platforms/architectures

.PHONY: dist-image-push
# Generates a list of image references in the form of
# "$(IMAGE_REPO):$(IMAGE_TAG)-{platform}-{arch}" generated from a list of "image_{platform}_{arch}"
ALL_IMAGE_REFS=$(subst image-,$(IMAGE_REPO):$(IMAGE_TAG)-,$(subst _,-,$(ALL_SUPPORTED_IMG_OS_ARCH)))
# Depends on "push_image_{platform}_{arch}" targets
dist-image-push: dist-image  $(subst image_,push_image_,$(ALL_SUPPORTED_IMG_OS_ARCH)) ## Push images and image manifest for all supported platforms
# Push all built images from the "image_{platform}_{arch}" target
	@for img in $(ALL_IMAGE_REFS); do podman push $(PODMAN_OPTS) $$img; done
# If the manifest with the same tag exists we need to remove it first, otherwise
# podman manifest create fails
	@2>/dev/null 1>/dev/null podman manifest rm $(IMAGE_REPO):$(IMAGE_TAG) || true
	@podman manifest create $(IMAGE_REPO):$(IMAGE_TAG)
# We set the TARGETOS and TARGETARCH from the image reference, given the
# convention of having the image reference be tagged with "{tag}-{platform}-{arch}"
	@for img in $(ALL_IMAGE_REFS); do TARGETOS=$$(echo $$img | sed -e 's/.*:[^-]\+-\([^-]\+\).*/\1/'); TARGETARCH=$${img/*-}; podman manifest add $(IMAGE_REPO):$(IMAGE_TAG) $(PODMAN_OPTS) $$img --os $${TARGETOS} --arch $${TARGETARCH}; done
	@podman manifest push $(IMAGE_REPO):$(IMAGE_TAG) $(IMAGE_REPO):$(IMAGE_TAG)

.PHONY: dev
dev: REGISTRY_PORT=5000
dev: IMAGE_REPO=localhost:$(REGISTRY_PORT)/ec
dev: PODMAN_OPTS=--tls-verify=false
dev: TASK_REPO=localhost:$(REGISTRY_PORT)/ec-task-bundle
dev: TASK:=$(shell T=$$(mktemp) && yq e ".spec.steps[].image? = \"127.0.0.1:$(REGISTRY_PORT)/ec\"" task/*/verify-enterprise-contract.yaml > "$${T}" && echo "$${T}")
dev: push-image task-bundle ## Push the ec-cli and v-e-c Task Bundle to the kind cluster setup via hack/setup-dev-environment.sh
	@rm "$(TASK)"

TASK_TAG ?= latest
TASK_REPO ?= quay.io/hacbs-contract/ec-task-bundle
TASK_VERSION ?= 0.1
TASK ?= task/$(TASK_VERSION)/verify-enterprise-contract.yaml
.PHONY: task-bundle
task-bundle: ## Push the Tekton Task bundle an image repository
# TODO using .fake-kube-config as `tkn bundle push` initializes the k8s client
# and that requires a valid Kubernetes config file, we're providing a fake one
# as connecting to a cluster is not required, only a valid config file is
# See https://github.com/tektoncd/cli/pull/1807
	@KUBECONFIG=$(dir $(abspath $(lastword $(MAKEFILE_LIST))))/.fake-kube-config go run -modfile tools/go.mod github.com/tektoncd/cli/cmd/tkn bundle push $(TASK_REPO):$(TASK_TAG) -f $(TASK)

.PHONY: task-bundle-snapshot
task-bundle-snapshot: task-bundle ## Push task bundle and then tag with "snapshot"
	@skopeo copy "docker://$(TASK_REPO):$(TASK_TAG)" "docker://$(TASK_REPO):snapshot"
