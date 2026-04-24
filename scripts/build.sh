#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-dev}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${ROOT_DIR}/build"

rm -rf "${BUILD_DIR}"
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

package_one() {
  local goos="$1"
  local goarch="$2"
  local suffix=""
  if [[ "${goos}" == "windows" ]]; then
    suffix=".exe"
  fi

  local package_name="opencode-agent-${goos}-${goarch}"
  local binary="${BUILD_DIR}/${package_name}${suffix}"
  local staging
  staging="$(mktemp -d)"
  local root="${staging}/${package_name}"

  mkdir -p "${root}"
  cp "${binary}" "${root}/opencode-agent${suffix}"
  cp "${ROOT_DIR}/README.md" "${ROOT_DIR}/LICENSE" "${root}/"
  if [[ -d "${ROOT_DIR}/skills" ]]; then
    mkdir -p "${root}/skills"
    cp -R "${ROOT_DIR}/skills/." "${root}/skills/"
  fi

  if [[ "${goos}" == "windows" ]]; then
    (cd "${staging}" && zip -qr "${BUILD_DIR}/${package_name}.zip" "${package_name}")
  else
    (cd "${staging}" && tar -czf "${BUILD_DIR}/${package_name}.tar.gz" "${package_name}")
  fi
  rm -rf "${staging}"
}

cd "${ROOT_DIR}"
go mod download
build_one darwin arm64
build_one darwin amd64
build_one linux amd64
build_one linux arm64
build_one windows amd64

package_one darwin arm64
package_one darwin amd64
package_one linux amd64
package_one linux arm64
package_one windows amd64

shasum -a 256 "${BUILD_DIR}"/opencode-agent-* > "${BUILD_DIR}/SHA256SUMS"
cat "${BUILD_DIR}/SHA256SUMS"
