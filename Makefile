SHELL := /bin/bash

VERBOSE ?= 0
KERNEL_MODE ?= source
BOOT_MODE ?= linux
AGENT_PORT ?= 8080
MEMORY_MIB ?= 512
CPUS ?= 2

ROOT_IMAGE ?= build/rootfs.raw
KERNEL ?= build/kernel
AGENT_GO_SOURCES := $(shell find agent -type f -name '*.go')

.PHONY: help agent rootfs kernel raw image run run-efi clean clean-kernel check-kernel require-kernel

help:
	@echo "Targets:"
	@echo "  make image              Build agent + rootfs + kernel + raw image"
	@echo "  make kernel             Build/refresh kernel artifact"
	@echo "  make rootfs             Build/refresh rootfs tree"
	@echo "  make raw                Build/refresh rootfs.raw"
	@echo "  make run                Run VM in linux boot mode"
	@echo "  make run-efi            Run VM in efi boot mode"
	@echo "  make check-kernel       Validate kernel artifact format"
	@echo ""
	@echo "Variables:"
	@echo "  VERBOSE=1               Verbose script output"
	@echo "  KERNEL_MODE=source      Force source kernel build"
	@echo "  AGENT_PORT=8080         Agent port"
	@echo "  MEMORY_MIB=512 CPUS=2   VM resources"

build/.agent.stamp: image/scripts/build-agent.sh image/scripts/lib/arch.sh agent/go.mod $(AGENT_GO_SOURCES)
	@mkdir -p build
	VERBOSE=$(VERBOSE) ./image/scripts/build-agent.sh
	@touch $@

agent: build/.agent.stamp

build/.rootfs.stamp: image/scripts/build-rootfs.sh image/scripts/lib/arch.sh image/rootfs-overlay/etc/hostname image/rootfs-overlay/etc/passwd image/rootfs-overlay/etc/group build/.agent.stamp
	VERBOSE=$(VERBOSE) ./image/scripts/build-rootfs.sh
	@touch $@

rootfs: build/.rootfs.stamp

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

run: build/rootfs.raw require-kernel
	cd manager && ./run-local.sh \
		--boot-mode $(BOOT_MODE) \
		--kernel ../$(KERNEL) \
		--root-image ../$(ROOT_IMAGE) \
		--agent-port $(AGENT_PORT) \
		--memory-mib $(MEMORY_MIB) \
		--cpus $(CPUS) \
		--verbose

# TODO: Untested and unsupported.
run-efi: build/rootfs.raw
	cd manager && ./run-local.sh \
		--boot-mode efi \
		--root-image ../$(ROOT_IMAGE) \
		--memory-mib $(MEMORY_MIB) \
		--cpus $(CPUS) \
		--verbose

clean-kernel:
	rm -f build/kernel build/vmlinuz build/vmlinuz-virt
	rm -rf build/kernel-src

clean:
	rm -rf build
	rm -rf manager/.build
