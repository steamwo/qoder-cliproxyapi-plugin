#!/usr/bin/env sh
set -eu
GOOS_VALUE="$(go env GOOS)"
GOARCH_VALUE="$(go env GOARCH)"
case "$GOOS_VALUE" in
  windows) ext=dll ;;
  darwin) ext=dylib ;;
  *) ext=so ;;
esac
out="dist/$GOOS_VALUE/$GOARCH_VALUE"
mkdir -p "$out"
CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o "$out/qoder.$ext" .
rm -f "$out/qoder.h"
echo "$out/qoder.$ext"
