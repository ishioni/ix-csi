# TrueNAS CSI Driver Makefile
# Includes targets for standard Kubernetes and OpenShift certification builds

# Image configuration
REGISTRY ?= quay.io/truenas_solutions
DRIVER_IMAGE ?= $(REGISTRY)/truenas-csi
VERSION ?= 1.1.2
IMG_TAG ?= v$(VERSION)

# Go configuration
GO ?= go
LDFLAGS ?= -X github.com/truenas/truenas-csi/pkg/driver.DRIVER_VERSION=$(IMG_TAG)
GOFLAGS ?= -v

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: build
build: ## Build the CSI driver binary
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o bin/truenas-csi cmd/main.go

.PHONY: test
test: ## Run unit tests
	$(GO) test ./... -v

.PHONY: test-sanity
test-sanity: ## Run CSI sanity tests
	$(GO) test ./test/sanity/... -v

.PHONY: lint
lint: ## Run linter
	golangci-lint run ./...

.PHONY: clean
clean: ## Clean build artifacts
	rm -rf bin/

.PHONY: bump-version
bump-version: ## Update all hardcoded version refs (e.g. make bump-version VERSION=1.1.3)
	@echo "Setting version to $(VERSION)"
	sed -i 's/^VERSION ?= .*/VERSION ?= $(VERSION)/' Makefile
	@echo "Done. Dockerfile 'version' labels are set from the VERSION build-arg at build time."

##@ Docker Builds (Standard)

.PHONY: docker-build
docker-build: ## Build standard Docker image (Alpine-based)
	docker build --build-arg VERSION=$(VERSION) -t $(DRIVER_IMAGE):$(IMG_TAG) .

.PHONY: docker-push
docker-push: ## Push standard Docker image
	docker push $(DRIVER_IMAGE):$(IMG_TAG)

.PHONY: push-latest
push-latest: ## Push the driver image with the 'latest' tag
	docker tag $(DRIVER_IMAGE):$(IMG_TAG) $(DRIVER_IMAGE):latest
	docker push $(DRIVER_IMAGE):latest

##@ Build All

.PHONY: build-all
build-all: docker-build ## Build the driver image

.PHONY: push-all
push-all: docker-push ## Push the versioned driver image

##@ Release

.PHONY: release
release: build-all push-all push-latest ## Build and push the driver image including the 'latest' tag
	@echo ""
	@echo "Release v$(VERSION) complete!"
	@echo "  - Driver: $(DRIVER_IMAGE):$(IMG_TAG) and :latest"
	@echo ""
	@echo "Next steps:"
	@echo "  1. Tag the git repository: git tag v$(VERSION) && git push --tags"
