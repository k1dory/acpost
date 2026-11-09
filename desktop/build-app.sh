#!/bin/sh
# Builds bin/acpost.app (macOS): the interface, the binary, the bundle, an ad-hoc signature.
set -eu
cd "$(dirname "$0")"
# npm ci: the lock file as it is. Vite empties dist, .keep (go:embed needs a file there) comes back.
(cd frontend && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null && touch dist/.keep)
export CGO_ENABLED=1 CGO_CFLAGS=-mmacosx-version-min=12.0 CGO_LDFLAGS=-mmacosx-version-min=12.0 MACOSX_DEPLOYMENT_TARGET=12.0
go build -tags production -trimpath -buildvcs=false -ldflags="-w -s" -o bin/acpost .
app=bin/acpost.app
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp build/darwin/icons.icns "$app/Contents/Resources/"
cp bin/acpost "$app/Contents/MacOS/acpost"
cp build/darwin/Info.plist "$app/Contents/"
xattr -cr "$app"
codesign --force --deep --sign - "$app"
codesign --verify --deep --strict "$app"
# A new icon is not shown while macOS has the bundle's old one cached: the bundle registered anew.
touch "$app"
/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f "$app" 2>/dev/null || true
echo "$app"
