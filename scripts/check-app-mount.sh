#!/usr/bin/env bash
# Render the app-bearing chart, materialize the ConfigMap exactly as the
# volume's `items` project it, and lint the result with the real runtime.
#
# This is the one check that spans the seam #110 was filed for: `app file=`
# resolves against the guardfile's directory, and until now nothing verified
# that the chart lands the widget where the runtime looks. Both halves lint
# clean on their own while the pod fails at startup.
set -euo pipefail

spec=examples/guardfile-siblings.mcp.kdl
widget=examples/widgets/things.html
widget_path=widgets/things.html
release=mcp-beaver

root=$(mktemp -d)
trap 'rm -rf "${root}"' EXIT

helm template "${release}" chart \
  --set-file "spec=${spec}" \
  --set-file "widgets[0].content=${widget}" \
  --set "widgets[0].path=${widget_path}" \
  >"${root}/rendered.yaml"

python3 scripts/materialize-spec-mount.py "${root}/rendered.yaml" "${root}/spec"

# The runtime ENTRYPOINT reads exactly this path, so lint reads it too.
go run ./cmd/mcp-beaver lint --apps "${root}/spec/${release}.mcp.kdl"
