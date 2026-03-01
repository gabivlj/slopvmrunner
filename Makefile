SHELL := /bin/bash

VERBOSE ?= 1
RUN_VERBOSE ?= 0
KERNEL_MODE ?= source
BOOT_MODE ?= linux
AGENT_VSOCK_PORT ?= 7000
MEMORY_MIB ?= 512
CPUS ?= 2
IMAGE ?= docker.io/library/ubuntu:latest

ROOT_IMAGE ?= build/rootfs.raw
KERNEL ?= build/kernel
AGENT_GO_SOURCES := $(shell find agent -type f -name '*.go')
VM_GO_SOURCES := $(shell find vm -type f -name '*.go')
API_CAPNP_SOURCES := $(shell find api/capnp -type f -name '*.capnp')
API_GO_GENERATED := $(shell find api/gen/go -type f -name '*.go' 2>/dev/null)
VMMANAGER_SIGNING_ENV := $(wildcard manager/.env.local)
GO_CACHE_DIR := $(abspath build/.gocache)
GO_PATH_DIR := $(abspath build/.gopath)
GO_BUILD_ENV := GOCACHE=$(GO_CACHE_DIR) GOPATH=$(GO_PATH_DIR)
GO_MIN_VERSION := go1.26.0
TEST ?=
VERBOSE_FLAG := $(if $(filter 1 true yes on,$(RUN_VERBOSE)),--verbose,)

.PHONY: help api agent rootfs kernel raw image vm-binaries test run run-go run-container run-efi clean clean-kernel check-kernel require-kernel check-go

help:
	@echo "Targets:"
	@echo "  make image              Build agent + rootfs + kernel + raw image"
	@echo "  make api                Generate Go bindings from api/capnp/*.capnp"
	@echo "  make vm-binaries        Build vm + vmmanager binaries into build/"
	@echo "  make kernel             Build/refresh kernel artifact"
	@echo "  make rootfs             Build/refresh rootfs tree"
	@echo "  make raw                Build/refresh rootfs.raw"
	@echo "  make run                Run VM in linux boot mode"
	@echo "  make run-go             Run VM via Go wrapper (spawns Swift manager)"
	@echo "  make run-container      Run VM via Go wrapper and auto-pull IMAGE for container flow"
	@echo "  make run-efi            Run VM in efi boot mode"
	@echo "  make test               Run e2e cold-boot benchmark test"
	@echo "  make check-kernel       Validate kernel artifact format"
	@echo ""
	@echo "Variables:"
	@echo "  VERBOSE=1               Verbose script output"
	@echo "  RUN_VERBOSE=0           Verbose VM runtime logs/console output"
	@echo "  KERNEL_MODE=source      Force source kernel build"
	@echo "  AGENT_VSOCK_PORT=7000   Agent vsock port"
	@echo "  IMAGE=docker.io/library/ubuntu:latest  Container image used by run-container"
	@echo "  TEST=Regex             Optional go test -run filter for make test"
	@echo "  MEMORY_MIB=512 CPUS=2   VM resources"

check-go:
	@v="$$(go env GOVERSION 2>/dev/null || true)"; \
	if [[ -z "$$v" ]]; then \
		echo "go toolchain not found in PATH"; \
		exit 1; \
	fi; \
	if ! printf '%s\n%s\n' "$(GO_MIN_VERSION)" "$$v" | sort -V -C; then \
		echo "go toolchain too old: $$v (need >= $(GO_MIN_VERSION))"; \
		exit 1; \
	fi

build/.api-go.stamp: api/scripts/gen-go.sh api/go.mod api/go.sum $(API_CAPNP_SOURCES)
	@mkdir -p build
	cd api && $(GO_BUILD_ENV) ./scripts/gen-go.sh
	@touch $@

api: check-go build/.api-go.stamp

build/agent: check-go image/scripts/build-agent.sh image/scripts/lib/arch.sh agent/go.mod $(AGENT_GO_SOURCES) api/scripts/gen-go.sh api/go.mod api/go.sum $(API_CAPNP_SOURCES)
	@mkdir -p build
	$(MAKE) api
	VERBOSE=$(VERBOSE) ./image/scripts/build-agent.sh

agent: build/agent

build/.rootfs.stamp: image/scripts/build-rootfs.sh image/scripts/lib/arch.sh image/rootfs-overlay/etc/hostname image/rootfs-overlay/etc/passwd image/rootfs-overlay/etc/group build/agent
	VERBOSE=$(VERBOSE) ./image/scripts/build-rootfs.sh
	@touch $@

rootfs: image/scripts/build-rootfs.sh image/scripts/lib/arch.sh image/rootfs-overlay/etc/hostname image/rootfs-overlay/etc/passwd image/rootfs-overlay/etc/group build/agent
	VERBOSE=$(VERBOSE) ./image/scripts/build-rootfs.sh
	@touch build/.rootfs.stamp

build/kernel: image/scripts/build-kernel.sh image/scripts/build-kernel-source.sh image/scripts/lib/arch.sh build/.rootfs.stamp
	VERBOSE=$(VERBOSE) KERNEL_MODE=$(KERNEL_MODE) ./image/scripts/build-kernel.sh
	@if [[ "$$(uname -m)" == "arm64" ]]; then \
		file build/kernel | grep -q "Linux kernel ARM64 boot executable Image" || \
		( echo "invalid kernel format for arm64:" && file build/kernel && exit 1 ); \
	fi

kernel: build/kernel

build/rootfs.raw: image/scripts/make-raw-image.sh build/.rootfs.stamp
	VERBOSE=$(VERBOSE) ./image/scripts/make-raw-image.sh

raw: build/rootfs.raw

image: build/kernel build/rootfs.raw

build/vmmanager: manager/run-local.sh manager/Package.swift manager/Sources/vmmanager/main.swift manager/vmmanager.entitlements manager/vmmanager.networking.entitlements $(VMMANAGER_SIGNING_ENV)
	@mkdir -p build
	./manager/run-local.sh --out "$(abspath build/vmmanager)" --build-only

build/vm: check-go vm/go.mod $(VM_GO_SOURCES) build/.api-go.stamp $(API_GO_GENERATED) build/vmmanager
	@mkdir -p build
	cd vm && $(GO_BUILD_ENV) go build -o ../build/vm ./cmd/vm

vm-binaries: build/vmmanager build/vm

test: check-go build/rootfs.raw require-kernel vm-binaries
	cd vm && $(GO_BUILD_ENV) go test -count=1 -v $(if $(TEST),-run '$(TEST)',) ./...

check-kernel: build/kernel
	@if [[ "$$(uname -m)" == "arm64" ]]; then \
		file build/kernel | grep -q "Linux kernel ARM64 boot executable Image" || \
		( echo "invalid kernel format for arm64:" && file build/kernel && exit 1 ); \
	fi

require-kernel:
	@if [[ ! -f "$(KERNEL)" ]]; then \
		echo "missing kernel artifact: $(KERNEL)"; \
		echo "run: make kernel"; \
		exit 1; \
	fi
	@if [[ "$$(uname -m)" == "arm64" ]]; then \
		file "$(KERNEL)" | grep -q "Linux kernel ARM64 boot executable Image" || \
		( echo "invalid kernel format for arm64:" && file "$(KERNEL)" && echo "run: make kernel KERNEL_MODE=source" && exit 1 ); \
	fi

run: build/rootfs.raw require-kernel build/vmmanager
	./build/vmmanager \
		--boot-mode $(BOOT_MODE) \
		--kernel $(KERNEL) \
		--root-image $(ROOT_IMAGE) \
		--agent-vsock-port $(AGENT_VSOCK_PORT) \
		--memory-mib $(MEMORY_MIB) \
		--cpus $(CPUS) \
		$(VERBOSE_FLAG)

run-go: build/rootfs.raw require-kernel build/vm
	./build/vm \
		--boot-mode $(BOOT_MODE) \
		--kernel $(KERNEL) \
		--root-image $(ROOT_IMAGE) \
		--agent-vsock-port $(AGENT_VSOCK_PORT) \
		--agent-ready-socket build/agent-ready.sock \
		--memory-mib $(MEMORY_MIB) \
		--cpus $(CPUS) \
		$(VERBOSE_FLAG)

run-container: build/rootfs.raw require-kernel build/vm
	./build/vm \
		--boot-mode $(BOOT_MODE) \
		--kernel $(KERNEL) \
		--root-image $(ROOT_IMAGE) \
		--agent-vsock-port $(AGENT_VSOCK_PORT) \
		--agent-ready-socket build/agent-ready.sock \
		--enable-network=true \
		--network-mode nat \
		--memory-mib $(MEMORY_MIB) \
		--cpus $(CPUS) \
		--container-image $(IMAGE) \
		$(VERBOSE_FLAG)

run-efi: build/rootfs.raw build/vmmanager
	./build/vmmanager \
		--boot-mode efi \
		--root-image $(ROOT_IMAGE) \
		--memory-mib $(MEMORY_MIB) \
		--cpus $(CPUS) \
		$(VERBOSE_FLAG)

clean-kernel:
	rm -f build/kernel build/vmlinuz build/vmlinuz-virt
	rm -rf build/kernel-src

clean:
	rm -rf build
	rm -rf manager/.build
