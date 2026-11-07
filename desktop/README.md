# acpost desktop

The macOS app of acpost (Wails v3, Svelte 5): conversations with agents, what they do as they do
it, approvals, changes, pull requests and CI, a terminal, usage. A client of the local node, as
the `acpost` command is: it reads the same configuration (`ACPOST_CONFIG`) and talks to the
node's socket (`ACPOST_NODE`/`ACPOST_TOKEN` for another node).

```sh
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
cd desktop/frontend && npm install && npm run build && cd ..
./build-app.sh                      # bin/acpost.app (open it, or drag it to /Applications)
wails3 task dev                     # live reload while working on the interface
```

`go build -tags server` builds the same app as a local HTTP server (http://localhost:8080) for
looking at the interface in a browser. Bindings in `frontend/bindings` are generated:
`wails3 generate bindings -ts -d frontend/bindings ./`.

The IDE view is VS Code (the one installed on the Mac runs `serve-web`, pinned to `ideCommit` in
`ide.go`; its server, settings and extensions live in `~/Library/Application Support/acpost/vscode`)
dressed as the app by the extension in `ide/extension`: colour themes made from the app's tokens,
Lucide icons in place of VS Code's, the app's fonts and `workbench.css`. Its generated files are
committed; after changing the tokens or the icon map: `cd ide && npm install && npm run build`.
