#!/usr/bin/env bash
# Builds beenfard from the beenfar commit that the required api module
# version comes from, so the acceptance tests run against the controller the
# provider was written for. Prints the binary's path.
#
#   api/vX.Y.Z tag      -> that tag
#   pseudo-version      -> its commit (v0.0.0-<time>-<hash>)
set -euo pipefail

out=${1:?usage: build-beenfard.sh OUTDIR}
v=$(go list -m -f '{{.Version}}' github.com/jacobalberty/beenfar/api)
case "$v" in
  *-*-*) ref=${v##*-} ;;
  *) ref="api/$v" ;;
esac
src="$out/beenfar-src"
rm -rf "$src"
git clone --quiet --filter=blob:none https://github.com/jacobalberty/beenfar.git "$src"
git -C "$src" checkout --quiet "$ref"
(cd "$src" && go build -o "$out/beenfard" ./cmd/beenfard)
echo "$out/beenfard"
