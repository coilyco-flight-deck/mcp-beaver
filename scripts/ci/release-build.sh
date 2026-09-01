#!/bin/sh
# Build the release binaries and their package metadata.
set -eu
just release-build
just release-package
