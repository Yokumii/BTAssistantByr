#!/usr/bin/env bash

set -euo pipefail

tag="${1:-}"
if [[ -z "${tag}" ]]; then
  echo "usage: $0 <release-tag>" >&2
  exit 2
fi

if [[ "${tag}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  channel="stable"
  branch="main"
  prerelease="false"
elif [[ "${tag}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+-(alpha|beta|rc)(\.[0-9A-Za-z-]+)*$ ]]; then
  channel="prerelease"
  branch="dev"
  prerelease="true"
else
  echo "invalid release tag: ${tag}" >&2
  echo "expected vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-{alpha|beta|rc}[.IDENTIFIER]" >&2
  exit 1
fi

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  {
    echo "channel=${channel}"
    echo "branch=${branch}"
    echo "prerelease=${prerelease}"
  } >>"${GITHUB_OUTPUT}"
fi

echo "tag=${tag} channel=${channel} branch=${branch} prerelease=${prerelease}"
