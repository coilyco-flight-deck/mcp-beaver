#!/usr/bin/env bash
set -euo pipefail

# The registry path moved to mcp-beaver first and the rest of the name followed
# later, so images published either side of that are all under this path and all
# still pullable. A deploy values file pins a source SHA rather than a tag, so
# nothing here decides when a consumer adopts a new build.
registry="forgejo.coilysiren.me"
image_name="coilyco-flight-deck/mcp-beaver"

if [ -z "${REGISTRY_TOKEN:-}" ]; then
  echo "REGISTRY_TOKEN is required for the trusted image-publish lane." >&2
  exit 1
fi

sha="${GITHUB_SHA:-$(git rev-parse HEAD)}"
case "${sha}" in
  *[!0-9a-f]*|"")
    echo "mcp-beaver source sha is not a lowercase hexadecimal commit id." >&2
    exit 1
    ;;
esac
if [ "${#sha}" -ne 40 ]; then
  echo "mcp-beaver source sha must be a full 40-character commit id." >&2
  exit 1
fi

image="${registry}/${image_name}:${sha}"
docker_config="$(mktemp -d)"
trap 'rm -rf "${docker_config}"' EXIT
chmod 700 "${docker_config}"
export DOCKER_CONFIG="${docker_config}"

printf '%s' "${REGISTRY_TOKEN}" \
  | docker login "${registry}" --username coilyco-ops --password-stdin

echo "==> building ${image}"
docker build --pull -t "${image}" .

echo "==> publishing ${image}"
docker push "${image}"

docker manifest inspect "${image}" >/dev/null
echo "verified immutable manifest ${image}"
