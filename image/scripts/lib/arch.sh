#!/usr/bin/env bash
set -euo pipefail

host_arch() {
  uname -m
}

to_goarch() {
  case "${1:-$(host_arch)}" in
    arm64|aarch64) echo "arm64" ;;
    x86_64|amd64) echo "amd64" ;;
    *)
      echo "unsupported host arch: ${1:-$(host_arch)}" >&2
      return 1
      ;;
  esac
}

to_alpine_arch() {
  case "${1:-$(host_arch)}" in
    arm64|aarch64) echo "aarch64" ;;
    x86_64|amd64) echo "x86_64" ;;
    *)
      echo "unsupported host arch: ${1:-$(host_arch)}" >&2
      return 1
      ;;
  esac
}

to_runc_arch() {
  case "${1:-$(host_arch)}" in
    arm64|aarch64) echo "arm64" ;;
    x86_64|amd64) echo "amd64" ;;
    *)
      echo "unsupported host arch: ${1:-$(host_arch)}" >&2
      return 1
      ;;
  esac
}
