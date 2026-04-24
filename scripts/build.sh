#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-dev}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${ROOT_DIR}/build"

mkdir -p "${BUILD_DIR}"

build_one() {
  local goos="$1"
  local goarch="$2"
  local suffix=""
  if [[ "${goos}" == "windows" ]]; then
    suffix=".exe"
  fi
  local output="${BUILD_DIR}/opencode-agent-${goos}-${goarch}${suffix}"
  echo "Building ${output}"
  GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 \
    go build \
      -ldflags "-s -w -X github.com/Stonefish-Labs/opencode-agent/internal/cli.Version=${VERSION}" \
      -o "${output}" \
      ./cmd/opencode-agent
}

cd "${ROOT_DIR}"
go mod download
build_one darwin arm64
build_one darwin amd64
build_one linux amd64
build_one linux arm64
build_one windows amd64

shasum -a 256 "${BUILD_DIR}"/opencode-agent-* > "${BUILD_DIR}/SHA256SUMS"
cat "${BUILD_DIR}/SHA256SUMS"
