#!/bin/sh
# Installs acpost from this checkout: the command into a directory of PATH, and what it needs said.
#
#   ./install.sh              the acpost command into ~/.local/bin (ACPOST_BIN names another)
#   ./install.sh --adapters   the ACP adapters too (npm i -g: claude-agent-acp, codex-acp)
#   ./install.sh --app        macOS: the app too, into /Applications (ACPOST_APPS names another)
#
# The app carries its own acpost for the agents it starts; this is the command for your terminal.
set -eu
cd "$(dirname "$0")"
adapters= app=
for a in "$@"; do
	case $a in
	--adapters) adapters=1 ;;
	--app) app=1 ;;
	*) echo "usage: ./install.sh [--adapters] [--app]" >&2; exit 2 ;;
	esac
done
command -v go >/dev/null || { echo "install.sh: Go is needed (https://go.dev/dl)" >&2; exit 1; }

bin=${ACPOST_BIN:-$HOME/.local/bin}
mkdir -p "$bin"
# Built beside and moved over: a node running the old command keeps running it.
CGO_ENABLED=0 go build -trimpath -ldflags="-w -s" -o "$bin/.acpost.new" ./cmd/acpost
mv -f "$bin/.acpost.new" "$bin/acpost"
echo "acpost: $bin/acpost"
case ":$PATH:" in
*":$bin:"*) ;;
*) echo "  $bin is not on your PATH: add  export PATH=\"$bin:\$PATH\"  to your shell's profile" ;;
esac
found=$(command -v acpost || true)
if [ -n "$found" ] && [ "$found" != "$bin/acpost" ]; then
	echo "  another acpost comes first on your PATH: $found"
fi

missing=
for cmd in claude-agent-acp codex-acp; do
	command -v "$cmd" >/dev/null || missing="$missing @agentclientprotocol/$cmd"
done
if [ -n "$missing" ] && [ -n "$adapters" ]; then
	command -v npm >/dev/null || { echo "install.sh: npm is needed for the adapters" >&2; exit 1; }
	# shellcheck disable=SC2086
	npm i -g $missing
elif [ -n "$missing" ]; then
	echo "adapters not found (an agent needs its own):  npm i -g$missing   (or ./install.sh --adapters)"
fi

if [ -n "$app" ]; then
	[ "$(uname)" = Darwin ] || { echo "install.sh: --app is for macOS" >&2; exit 1; }
	apps=${ACPOST_APPS:-/Applications}
	built=$(./desktop/build-app.sh | tail -1)
	rm -rf "$apps/acpost.app.new"
	cp -R "desktop/$built" "$apps/acpost.app.new"
	rm -rf "$apps/acpost.app"
	mv "$apps/acpost.app.new" "$apps/acpost.app"
	echo "app: $apps/acpost.app (quit and open it again to run the new one)"
fi
