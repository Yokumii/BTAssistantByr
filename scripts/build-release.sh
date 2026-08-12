#!/usr/bin/env bash

set -euo pipefail

tag="${1:-}"
output_dir="${2:-}"
if [[ -z "${tag}" || -z "${output_dir}" ]]; then
  echo "usage: $0 <release-tag> <output-directory>" >&2
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repository_dir="$(cd "${script_dir}/.." && pwd)"
cd "${repository_dir}"

"${script_dir}/release-metadata.sh" "${tag}" >/dev/null
version="${tag#v}"
if [[ -d "${output_dir}" && -n "$(ls -A "${output_dir}")" ]]; then
  echo "output directory must be empty: ${output_dir}" >&2
  exit 1
fi
mkdir -p "${output_dir}"
output_dir="$(cd "${output_dir}" && pwd)"

targets=(
  "linux amd64 tar.gz"
  "linux arm64 tar.gz"
  "darwin amd64 tar.gz"
  "darwin arm64 tar.gz"
  "windows amd64 zip"
  "windows arm64 zip"
)

for target in "${targets[@]}"; do
  read -r goos goarch archive_format <<<"${target}"
  package_name="BTAssistantByr_${version}_${goos}_${goarch}"
  staging_dir="$(mktemp -d)"
  trap 'rm -r "${staging_dir}"' EXIT
  binary_name="btassistantbyr"
  if [[ "${goos}" == "windows" ]]; then
    binary_name+=".exe"
  fi

  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${tag}" \
    -o "${staging_dir}/${binary_name}" \
    ./cmd/btassistantbyr
  cp LICENSE README.md config/config.example.yaml "${staging_dir}/"

  if [[ "${archive_format}" == "zip" ]]; then
    (
      cd "${staging_dir}"
      zip -q -r "${output_dir}/${package_name}.zip" .
    )
  else
    tar -C "${staging_dir}" -czf "${output_dir}/${package_name}.tar.gz" .
  fi
  rm -r "${staging_dir}"
  trap - EXIT
done

(
  cd "${output_dir}"
  checksum_files=(BTAssistantByr_*.tar.gz BTAssistantByr_*.zip)
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${checksum_files[@]}" >SHA256SUMS
  else
    shasum -a 256 "${checksum_files[@]}" >SHA256SUMS
  fi
)
