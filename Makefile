SHELL := /bin/bash

VERBOSE ?= 1
RUN_VERBOSE ?= 0
KERNEL_MODE ?= source
BOOT_MODE ?= linux
AGENT_VSOCK_PORT ?= 7000
MEMORY_MIB ?= 512
CPUS ?= 2
IMAGE ?= docker.io/library/ubuntu:latest

STATE_HOME ?= $(HOME)/.slopvmrunner
STATE_WORK ?= $(CURDIR)/.slopvmrunner
VM_NAME ?= devvm
ROOT_IMAGE ?= $(STATE_HOME)/rootfs/default.raw
KERNEL ?= $(STATE_HOME)/kernels/default
KERNEL_PREBUILT_DIR ?=
KERNEL_PREBUILT_FILE ?=
VM_BIN ?= $(STATE_HOME)/bin/vm
VMMANAGER_BIN ?= $(STATE_HOME)/bin/vmmanager
AGENT_GO_SOURCES := $(shell find agent -type f -name '*.go')
VM_GO_SOURCES := $(shell find vm -type f -name '*.go')
API_CAPNP_SOURCES := $(shell find api/capnp -type f -name '*.capnp')
API_GO_GENERATED := $(shell find api/gen/go -type f -name '*.go' 2>/dev/null)
VMMANAGER_SIGNING_ENV := $(wildcard manager/.env.local)
GO_CACHE_DIR := $(abspath $(STATE_HOME)/.gocache)
GO_PATH_DIR := $(abspath $(STATE_HOME)/.gopath)
GO_BUILD_ENV := GOCACHE=$(GO_CACHE_DIR) GOPATH=$(GO_PATH_DIR)
GO_MIN_VERSION := go1.26.0
TEST ?=
VERBOSE_FLAG := $(if $(filter 1 true yes on,$(RUN_VERBOSE)),--verbose,)

.PHONY: help api agent rootfs kernel raw image vm-binaries install test run run-go run-go-fast run-container run-container-fast run-efi clean clean-kernel check-kernel require-kernel require-installed-artifacts check-go

help:
	@echo "Targets:"
	@echo "  make image              Build agent + rootfs + kernel + raw image"
	@echo "  make api                Generate Go bindings from api/capnp/*.capnp"
	@echo "  make vm-binaries        Build vm + vmmanager binaries into ~/.slopvmrunner/bin/"
	@echo "  make install            Install flow (currently broken)"
	@echo "  make kernel             Build/refresh kernel artifact"
	@echo "  make rootfs             Build/refresh rootfs tree"
	@echo "  make raw                Build/refresh rootfs.raw"
	@echo "  make run                Run VM in linux boot mode"
	@echo "  make run-go             Run VM via Go wrapper (rebuilds changed artifacts)"
	@echo "  make run-go-fast        Run VM via Go wrapper using preinstalled artifacts only"
	@echo "  make run-container      Run container flow (rebuilds changed artifacts)"
	@echo "  make run-container-fast Run container flow using preinstalled artifacts only"
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
	@echo "  STATE_HOME=$(STATE_HOME)  Global install artifacts"
	@echo "  STATE_WORK=$(STATE_WORK)  Per-workdir VM state root"
	@echo "  VM_NAME=$(VM_NAME)        VM state name under STATE_WORK/vms"
	@echo "  KERNEL_PREBUILT_DIR=<dir> Import prebuilt kernel from a directory (skips kernel build)"
	@echo "  KERNEL_PREBUILT_FILE=<file> Exact file inside KERNEL_PREBUILT_DIR to copy (optional)"

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

$(STATE_HOME)/.api-go.stamp: api/scripts/gen-go.sh api/go.mod api/go.sum $(API_CAPNP_SOURCES)
	@mkdir -p $(STATE_HOME)
	cd api && $(GO_BUILD_ENV) ./scripts/gen-go.sh
	@touch $@

api: check-go $(STATE_HOME)/.api-go.stamp

$(STATE_HOME)/agent: check-go image/scripts/build-agent.sh image/scripts/lib/arch.sh agent/go.mod $(AGENT_GO_SOURCES) api/scripts/gen-go.sh api/go.mod api/go.sum $(API_CAPNP_SOURCES)
	@mkdir -p $(STATE_HOME)
	$(MAKE) api
	BUILD_DIR=$(STATE_HOME) VERBOSE=$(VERBOSE) ./image/scripts/build-agent.sh

agent: $(STATE_HOME)/agent

$(STATE_HOME)/.rootfs.stamp: image/scripts/build-rootfs.sh image/scripts/lib/arch.sh image/rootfs-overlay/etc/hostname image/rootfs-overlay/etc/passwd image/rootfs-overlay/etc/group $(STATE_HOME)/agent
	BUILD_DIR=$(STATE_HOME) VERBOSE=$(VERBOSE) ./image/scripts/build-rootfs.sh
	@touch $@

rootfs: image/scripts/build-rootfs.sh image/scripts/lib/arch.sh image/rootfs-overlay/etc/hostname image/rootfs-overlay/etc/passwd image/rootfs-overlay/etc/group $(STATE_HOME)/agent
	BUILD_DIR=$(STATE_HOME) VERBOSE=$(VERBOSE) ./image/scripts/build-rootfs.sh
	@touch $(STATE_HOME)/.rootfs.stamp

$(KERNEL): image/scripts/build-kernel.sh image/scripts/build-kernel-source.sh image/scripts/lib/arch.sh | $(STATE_HOME)/.rootfs.stamp
	@if [[ -n "$(KERNEL_PREBUILT_DIR)" ]]; then \
		mkdir -p "$(dir $(KERNEL))"; \
		if [[ -f "$(KERNEL_PREBUILT_DIR)" ]]; then \
			src="$(KERNEL_PREBUILT_DIR)"; \
		elif [[ -n "$(KERNEL_PREBUILT_FILE)" ]]; then \
			src="$(KERNEL_PREBUILT_DIR)/$(KERNEL_PREBUILT_FILE)"; \
			if [[ ! -f "$$src" ]]; then \
				echo "missing prebuilt kernel file: $$src"; \
				exit 1; \
			fi; \
		else \
			src=""; \
			for cand in kernel Image vmlinuz vmlinuz-virt; do \
				if [[ -f "$(KERNEL_PREBUILT_DIR)/$$cand" ]]; then src="$(KERNEL_PREBUILT_DIR)/$$cand"; break; fi; \
			done; \
			if [[ -z "$$src" ]]; then \
				echo "no kernel file found in $(KERNEL_PREBUILT_DIR); looked for: kernel, Image, vmlinuz, vmlinuz-virt"; \
				exit 1; \
			fi; \
		fi; \
		cp "$$src" "$(KERNEL)"; \
		echo "imported prebuilt kernel: $$src -> $(KERNEL)"; \
	else \
		BUILD_DIR=$(STATE_HOME) VERBOSE=$(VERBOSE) KERNEL_MODE=$(KERNEL_MODE) ./image/scripts/build-kernel.sh; \
	fi
	@if [[ "$$(uname -m)" == "arm64" ]]; then \
		file $(KERNEL) | grep -q "Linux kernel ARM64 boot executable Image" || \
		( echo "invalid kernel format for arm64:" && file $(KERNEL) && exit 1 ); \
	fi

kernel: $(KERNEL)

$(ROOT_IMAGE): image/scripts/make-raw-image.sh $(STATE_HOME)/.rootfs.stamp
	BUILD_DIR=$(STATE_HOME) VERBOSE=$(VERBOSE) ./image/scripts/make-raw-image.sh "$(ROOT_IMAGE)"

raw: $(ROOT_IMAGE)

image: $(KERNEL) $(ROOT_IMAGE)

$(VMMANAGER_BIN): manager/run-local.sh manager/Package.swift manager/Sources/vmmanager/main.swift manager/vmmanager.entitlements manager/vmmanager.networking.entitlements $(VMMANAGER_SIGNING_ENV)
	@mkdir -p "$(dir $(VMMANAGER_BIN))"
	./manager/run-local.sh --out "$(abspath $(VMMANAGER_BIN))" --build-only

$(VM_BIN): check-go vm/go.mod $(VM_GO_SOURCES) $(STATE_HOME)/.api-go.stamp $(API_GO_GENERATED) $(VMMANAGER_BIN)
	@mkdir -p "$(dir $(VM_BIN))"
	cd vm && $(GO_BUILD_ENV) go build -o "$(VM_BIN)" ./cmd/vm

vm-binaries: $(VMMANAGER_BIN) $(VM_BIN)

install:
	@echo "install is broken"
	@exit 1

test: check-go $(ROOT_IMAGE) require-kernel vm-binaries
	cd vm && $(GO_BUILD_ENV) go test -count=1 -v $(if $(TEST),-run '$(TEST)',) ./...

check-kernel: $(KERNEL)
	@if [[ "$$(uname -m)" == "arm64" ]]; then \
		file $(KERNEL) | grep -q "Linux kernel ARM64 boot executable Image" || \
		( echo "invalid kernel format for arm64:" && file $(KERNEL) && exit 1 ); \
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

require-installed-artifacts:
	@if [[ ! -x "$(VM_BIN)" ]]; then \
		echo "missing vm binary: $(VM_BIN)"; \
		echo "build artifacts first: make image vm-binaries"; \
		exit 1; \
	fi
	@if [[ ! -x "$(VMMANAGER_BIN)" ]]; then \
		echo "missing vmmanager binary: $(VMMANAGER_BIN)"; \
		echo "build artifacts first: make image vm-binaries"; \
		exit 1; \
	fi
	@if [[ ! -f "$(ROOT_IMAGE)" ]]; then \
		echo "missing root image: $(ROOT_IMAGE)"; \
		echo "build artifacts first: make image"; \
		exit 1; \
	fi
	@if [[ ! -f "$(KERNEL)" ]]; then \
		echo "missing kernel: $(KERNEL)"; \
		echo "build artifacts first: make kernel"; \
		exit 1; \
	fi

run: require-installed-artifacts require-kernel
	$(VMMANAGER_BIN) \
		--boot-mode $(BOOT_MODE) \
		--kernel $(KERNEL) \
		--root-image $(ROOT_IMAGE) \
		--agent-vsock-port $(AGENT_VSOCK_PORT) \
		--memory-mib $(MEMORY_MIB) \
		--cpus $(CPUS) \
		$(VERBOSE_FLAG)

run-go-fast: require-installed-artifacts require-kernel
	$(VM_BIN) \
		--boot-mode $(BOOT_MODE) \
		--kernel $(KERNEL) \
		--root-image $(ROOT_IMAGE) \
		--agent-vsock-port $(AGENT_VSOCK_PORT) \
		--home-state-root $(STATE_HOME) \
		--work-state-root $(STATE_WORK) \
		--vm-name $(VM_NAME) \
		--vmmanager $(VMMANAGER_BIN) \
		--memory-mib $(MEMORY_MIB) \
		--cpus $(CPUS) \
		$(VERBOSE_FLAG)

run-container-fast: require-installed-artifacts require-kernel
	$(VM_BIN) \
		--boot-mode $(BOOT_MODE) \
		--kernel $(KERNEL) \
		--root-image $(ROOT_IMAGE) \
		--agent-vsock-port $(AGENT_VSOCK_PORT) \
		--home-state-root $(STATE_HOME) \
		--work-state-root $(STATE_WORK) \
		--vm-name $(VM_NAME) \
		--vmmanager $(VMMANAGER_BIN) \
		--enable-network=true \
		--network-mode nat \
		--memory-mib $(MEMORY_MIB) \
		--cpus $(CPUS) \
		--container-image $(IMAGE) \
		$(VERBOSE_FLAG)

run-go: image vm-binaries
	$(MAKE) run-go-fast

run-container: image vm-binaries
	$(MAKE) run-container-fast

run-efi: require-installed-artifacts
	$(VMMANAGER_BIN) \
		--boot-mode efi \
		--root-image $(ROOT_IMAGE) \
		--memory-mib $(MEMORY_MIB) \
		--cpus $(CPUS) \
		$(VERBOSE_FLAG)

clean-kernel:
	rm -f $(STATE_HOME)/kernels/default $(STATE_HOME)/vmlinuz $(STATE_HOME)/vmlinuz-virt
	rm -rf $(STATE_HOME)/kernel-src

clean:
	rm -rf $(STATE_HOME)
	rm -rf $(STATE_WORK)
	rm -rf manager/.build
