# Image URL to use all building/pushing image targets
IMG ?= controller:latest

# Pinned tools, run through `go run` so there is nothing to install.
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.22.0
KUSTOMIZE ?= go run sigs.k8s.io/kustomize/kustomize/v5@v5.8.2
SETUP_ENVTEST ?= go run sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.25.2
# Kubernetes version of the envtest control plane (cert-manager 1.21 supports 1.33 to 1.36).
ENVTEST_K8S_VERSION ?= 1.36.x

all: manager

# Run tests (needs the envtest binaries, downloaded on first use)
test: generate fmt vet manifests
	assets="$$($(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" && \
		KUBEBUILDER_ASSETS="$$assets" go test ./... -coverprofile cover.out

# Build manager binary
manager: generate fmt vet
	go build -o bin/manager main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet manifests
	go run ./main.go

# Install CRDs into a cluster
install: manifests
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

# Uninstall CRDs from a cluster
uninstall: manifests
	$(KUSTOMIZE) build config/crd | kubectl delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | kubectl apply -f -

# Render the default kustomization
build-manifests:
	@$(KUSTOMIZE) build config/default

# Generate manifests e.g. CRD, RBAC etc.
manifests:
	$(CONTROLLER_GEN) crd rbac:roleName=manager-role paths="./..." output:crd:artifacts:config=config/crd/bases

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# Generate code
generate:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

# Build the docker image
docker-build:
	docker build . -t ${IMG}

# Push the docker image
docker-push:
	docker push ${IMG}

.PHONY: all test manager run install uninstall deploy build-manifests manifests fmt vet generate docker-build docker-push
