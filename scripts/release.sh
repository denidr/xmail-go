#!/usr/bin/env bash
# xmail release build script — builds distributable artifacts for the
# 3 release targets:
#   docker-amd64   linux/amd64 Docker image  (tar.gz, docker load-able)
#   docker-arm64   linux/arm64 Docker image  (Armbian SBCs; tar.gz)
#   windows-amd64  Windows x64 tray+service .exe
#
# All artifacts land in dist/, plus a SHA256SUMS.txt covering them.
# Safe to re-run: each target is independent and overwrites its own
# output only.
#
# Usage:
#   scripts/release.sh <target> [version]
#   scripts/release.sh all
#   VERSION=v0.1.0 scripts/release.sh all
#
# target: docker-amd64 | docker-arm64 | windows-amd64 | all
# version: defaults to `git describe --tags --always --dirty`, or
#          "dev" if that fails (e.g. no tags yet, no git repo).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

DIST_DIR="$ROOT_DIR/dist"
IMAGE="${DOCKER_IMAGE:-xmail}"

TARGET="${1:-}"
VERSION="${2:-${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}}"

usage() {
	cat >&2 <<EOF
Usage: $(basename "$0") <target> [version]

Targets:
  docker-amd64    Build & save linux/amd64 Docker image to dist/
  docker-arm64    Build & save linux/arm64 Docker image to dist/ (Armbian SBCs)
  windows-amd64   Build Windows x64 tray+service .exe to dist/
  all             Build all three, then write dist/SHA256SUMS.txt

Version defaults to 'git describe --tags --always --dirty', or "dev".

Examples:
  $(basename "$0") windows-amd64
  $(basename "$0") all v0.1.0
  VERSION=v0.1.0 $(basename "$0") all
EOF
}

log() { echo "==> $*" >&2; }

require_cmd() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "error: required command not found: $1" >&2
		exit 1
	fi
}

ensure_dist() { mkdir -p "$DIST_DIR"; }

# build_docker builds a single-platform Docker image via buildx and
# saves it as a gzip'd tarball in dist/ — a portable artifact that
# doesn't require a registry to distribute (`docker load < file.tar.gz`
# on the target machine).
build_docker() {
	local platform="$1" arch_label="$2"
	require_cmd docker
	ensure_dist

	local tag="$IMAGE:$VERSION-$arch_label"
	local out="$DIST_DIR/xmail-$VERSION-linux-$arch_label-docker.tar.gz"

	log "building docker $platform -> $tag"
	docker buildx build --platform "$platform" -t "$tag" --load "$ROOT_DIR"

	log "saving $tag -> $out"
	docker save "$tag" | gzip > "$out"
	log "done: $out ($(du -h "$out" | cut -f1))"
}

build_docker_amd64() { build_docker "linux/amd64" "amd64"; }

# Armbian SBCs (Orange Pi, Rock Pi, etc.) are overwhelmingly arm64
# these days; if targeting an older 32-bit board, build linux/arm/v7
# manually with: docker buildx build --platform linux/arm/v7 ...
build_docker_arm64() { build_docker "linux/arm64" "arm64"; }

build_windows_amd64() {
	require_cmd go
	ensure_dist

	local out="$DIST_DIR/xmail-tray-windows-amd64-$VERSION.exe"
	log "building windows/amd64 (tray + service) -> $out"
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
		go build -tags xmailtray \
		-ldflags="-s -w -H=windowsgui -X main.version=$VERSION" \
		-o "$out" "$ROOT_DIR/cmd/xmail-tray"
	log "done: $out ($(du -h "$out" | cut -f1))"
}

write_checksums() {
	ensure_dist
	( cd "$DIST_DIR" && rm -f SHA256SUMS.txt
	  if command -v sha256sum >/dev/null 2>&1; then
	    sha256sum -- * > SHA256SUMS.txt
	  else
	    shasum -a 256 -- * > SHA256SUMS.txt
	  fi )
	log "wrote $DIST_DIR/SHA256SUMS.txt"
}

case "$TARGET" in
	docker-amd64)
		build_docker_amd64
		write_checksums
		;;
	docker-arm64)
		build_docker_arm64
		write_checksums
		;;
	windows-amd64)
		build_windows_amd64
		write_checksums
		;;
	all)
		build_docker_amd64
		build_docker_arm64
		build_windows_amd64
		write_checksums
		;;
	-h|--help|"")
		usage
		exit 1
		;;
	*)
		echo "error: unknown target '$TARGET'" >&2
		usage
		exit 1
		;;
esac

log "release build complete (version=$VERSION)"
