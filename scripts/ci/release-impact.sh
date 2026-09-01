#!/bin/sh
# Classify release impact and publish the verdict as a step output.
set -eu
publish="$(just release-impact)"
echo "publish=${publish}" >> "${GITHUB_OUTPUT}"
