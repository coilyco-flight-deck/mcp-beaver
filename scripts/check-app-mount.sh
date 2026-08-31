#!/usr/bin/env bash
# Render the app-bearing chart, materialize the ConfigMap exactly as the
# volume's `items` project it, and lint the result with the real runtime.
#
# This is the one check that spans the seam #110 was filed for: `app file=`
# resolves against the guardfile's directory, and nothing else verifies that
# the chart lands the widget where the runtime looks. Both halves lint clean on
# their own while the pod fails at startup.
#
# yq rather than a Python YAML parser: dev-base carries yq at the same version
# CI and a laptop both resolve, and it carries no PyYAML in the runtime stage.
set -euo pipefail

spec=examples/guardfile-siblings.mcp.kdl
widget=examples/widgets/things.html
widget_path=widgets/things.html
release=mcp-beaver

root=$(mktemp -d)
trap 'rm -rf "${root}"' EXIT
rendered="${root}/rendered.yaml"
mount="${root}/spec"

helm template "${release}" chart \
  --set-file "spec=${spec}" \
  --set-file "widgets[0].content=${widget}" \
  --set "widgets[0].path=${widget_path}" \
  >"${rendered}"

# The item mapping is the thing under test, so the mount is built from it rather
# than from every ConfigMap key. See docs/apps.md.
items=$(yq -r '
  select(.kind == "Deployment")
  | .spec.template.spec.volumes[]
  | select(.name == "spec")
  | .configMap.items[]
  | .key + "\t" + .path
' "${rendered}")

if [ -z "${items}" ]; then
  echo "check-app-mount: the spec volume projects no items, so no mapping is under test" >&2
  exit 1
fi

while IFS=$'\t' read -r key path; do
  target="${mount}/${path}"
  mkdir -p "$(dirname "${target}")"
  binary=$(yq -r "select(.kind == \"ConfigMap\") | .binaryData[\"${key}\"] // \"\"" "${rendered}")
  text=$(yq -r "select(.kind == \"ConfigMap\") | .data[\"${key}\"] // \"\"" "${rendered}")
  if [ -n "${binary}" ]; then
    printf '%s' "${binary}" | base64 -d >"${target}"
  elif [ -n "${text}" ]; then
    printf '%s\n' "${text}" >"${target}"
  else
    echo "check-app-mount: the volume projects key ${key}, which the ConfigMap does not hold" >&2
    exit 1
  fi
done <<<"${items}"

# Byte-exact: base64 exists here to survive what a block scalar would rewrite,
# and a check tolerating a rewrite would not notice the encoding stopped.
if ! cmp -s "${widget}" "${mount}/${widget_path}"; then
  echo "check-app-mount: the mounted widget differs from ${widget}" >&2
  exit 1
fi

# The runtime ENTRYPOINT reads exactly this path, so lint reads it too.
go run ./cmd/mcp-beaver lint --apps "${mount}/${release}.mcp.kdl"
