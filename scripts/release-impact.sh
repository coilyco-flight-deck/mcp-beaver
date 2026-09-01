#!/bin/sh
# Decide whether one validated revision should publish a release.
#
# The train advances only on a diff that touches what it ships, so a docs or
# chart commit does not mint a version nobody can tell apart from the last one.
# The comparison runs from the previous release tag rather than the pushed
# range, so a revision that follows an unpublished one still publishes.
set -eu

event="${RELEASE_EVENT:-push}"
base="${RELEASE_BASE:-}"
head="${RELEASE_HEAD:-HEAD}"

if [ "$event" = "workflow_dispatch" ]; then
  echo true
  exit 0
fi

# A pending major holds automatic publication so a minor cannot take the
# number first. A dispatch is checked above and still cuts it. See docs/release.md.
if [ -f .release-major ]; then
  echo "release-impact: automatic publication held by .release-major" >&2
  echo false
  exit 0
fi

case "$base" in
  ""|0000000000000000000000000000000000000000)
    echo true
    exit 0
    ;;
esac

if ! git cat-file -e "${base}^{commit}" 2>/dev/null; then
  echo "release-impact: base revision is unavailable, publishing fail closed" >&2
  echo true
  exit 0
fi

impact_base="$base"
release_tag=$(git describe --tags --match 'v[0-9]*' --abbrev=0 "$head" 2>/dev/null || true)
if [ -n "$release_tag" ]; then
  impact_base="$release_tag"
fi

if git diff --quiet "$impact_base" "$head" -- \
  cmd \
  internal \
  go.mod \
  go.sum \
  scripts/release-build.sh \
  scripts/render-packaging.sh
then
  echo false
else
  status=$?
  if [ "$status" -ne 1 ]; then
    exit "$status"
  fi
  echo true
fi
