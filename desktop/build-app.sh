#!/bin/sh
# Builds bin/acpost.app (macOS): the interface, the binary, the bundle, an ad-hoc signature.
set -eu
cd "$(dirname "$0")"
(cd frontend && npm install --no-audit --no-fund >/dev/null && npm run build >/dev/null)
export CGO_ENABLED=1 CGO_CFLAGS=-mmacosx-version-min=12.0 CGO_LDFLAGS=-mmacosx-version-min=12.0 MACOSX_DEPLOYMENT_TARGET=12.0
go build -tags production -trimpath -buildvcs=false -ldflags="-w -s" -o bin/acpost .
app=bin/acpost.app
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp build/darwin/icons.icns "$app/Contents/Resources/"
[ -f build/darwin/Assets.car ] && cp build/darwin/Assets.car "$app/Contents/Resources/"
cp bin/acpost "$app/Contents/MacOS/acpost"
cp build/darwin/Info.plist "$app/Contents/"
xattr -cr "$app"
codesign --force --deep --sign - "$app"
codesign --verify --deep --strict "$app"
echo "$app"
