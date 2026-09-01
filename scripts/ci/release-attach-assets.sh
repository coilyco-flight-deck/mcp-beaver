#!/bin/sh
# Attach every built artifact to the created Forgejo release.
set -eu
for asset in \
  dist/mcp-beaver-darwin-arm64 \
  dist/mcp-beaver-darwin-amd64 \
  dist/mcp-beaver-linux-amd64 \
  dist/mcp-beaver-linux-arm64 \
  dist/SHA256SUMS \
  dist/mcp-beaver.rb
do
  name=$(basename "$asset")
  curl -fsSL -X POST \
    -H "Authorization: token ${FORGEJO_TOKEN}" \
    -F "attachment=@${asset}" \
    "${FORGEJO_API}/releases/${RELEASE_ID}/assets?name=${name}"
  echo "uploaded ${name}"
done
