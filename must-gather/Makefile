IMAGE_REGISTRY ?= quay.io
IMAGE_NAMESPACE ?= confidential-containers
IMAGE_NAME ?= trustee-must-gather
IMAGE_TAG ?= latest

MUST_GATHER_IMAGE ?= ${IMAGE_NAMESPACE}/${IMAGE_NAME}

build: podman-build

# Run shellcheck on collection scripts
check:
	shellcheck collection-scripts/*

# Build the must-gather image
podman-build:
	podman build --squash-all --no-cache . -t ${IMAGE_REGISTRY}/${MUST_GATHER_IMAGE}:${IMAGE_TAG}

# Push the must-gather image
podman-push:
	podman push ${IMAGE_REGISTRY}/${MUST_GATHER_IMAGE}:${IMAGE_TAG}

# Build and push
push: podman-build podman-push

# Build with docker
docker-build:
	docker build --no-cache . -t ${IMAGE_REGISTRY}/${MUST_GATHER_IMAGE}:${IMAGE_TAG}

# Push with docker
docker-push:
	docker push ${IMAGE_REGISTRY}/${MUST_GATHER_IMAGE}:${IMAGE_TAG}

.PHONY: build check podman-build podman-push push docker-build docker-push
