PREFIX            ?= $(shell pwd)
BIN_DIR           ?= $(shell pwd)
DOCKER_IMAGE_NAME ?= resque-exporter
DOCKER_IMAGE_TAG  ?= $(subst /,-,$(shell git rev-parse --abbrev-ref HEAD))
GOOS              ?= linux
GOARCH            ?= amd64

default: build

build:
	@echo ">> building binaries"
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) promu build --prefix $(PREFIX)

tarball:
	@echo ">> building release tarball"
	promu tarball --prefix $(PREFIX) $(BIN_DIR)

docker:
	@echo ">> building docker image"
	docker build -t "$(DOCKER_IMAGE_NAME):$(DOCKER_IMAGE_TAG)" .


.PHONY: build tarball docker
