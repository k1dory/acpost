# acpost desktop

The macOS app of acpost (Wails v3, Svelte 5): conversations with agents, what they do as they do
it, approvals, changes, pull requests and CI, a terminal, analytics. A client of the local node, as
the `acpost` command is: it reads the same configuration (`ACPOST_CONFIG`) and talks to the
node's socket (`ACPOST_NODE`/`ACPOST_TOKEN` for another node).

```sh
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
cd desktop
./build-app.sh                      # bin/acpost.app (open it, or drag it to /Applications)
wails3 task dev                     # live reload while working on the interface
```

`build-app.sh` is the one way the bundle is made: the interface from the lock file (`npm ci`), the
binary, `build/darwin/Info.plist`, an ad-hoc signature. The Taskfiles serve `wails3 task dev`.
The bundle's identity (`dev.acpost.desktop`, the version) is set in `build/config.yml`;
`wails3 task common:update:build-assets` writes it into the plists, which stay as it writes them.

`go build -tags server` builds the same app as a local HTTP server (http://localhost:8080) for
looking at the interface in a browser. Bindings in `frontend/bindings` are generated:
`wails3 generate bindings -ts -d frontend/bindings ./`.

Analytics has three tabs. Usage is tokens and cost by agent, model, day or session. Traces is where
agents' time went: the recent conversations with their active time, calls, errors and a bar of
phases (waiting to start, the model, tools, waiting for approval, other agents); a turn opened is a
timeline of its calls, permission waits and the messages it sent to other agents (each openable),
filtered by name, zoomed with ⌘ or ctrl and the wheel, a span's input and output beside it. The
node gives the timing (`GET /v1/traces`); the app reads a turn's events whole only when it is
opened, and `frontend/src/lib/trace.ts` makes the spans and phases. Commands groups the calls by what
they ran. A message's "Trace" opens its turn there.

The IDE view is VS Code (the one installed on the Mac runs `serve-web`, pinned to `ideCommit` in
`ide_ext.go`; its server, settings and extensions live in `~/Library/Application Support/acpost/vscode`)
dressed as the app by the extension in `ide/extension`: colour themes made from the app's tokens,
Lucide icons in place of VS Code's, the app's fonts and `workbench.css`; the app's VS Code settings
are the extension's `configurationDefaults` (the user's own win), the few application-wide ones
written by `ide/page/seed.js`. Its generated files are committed; after changing the tokens or the
icon map: `cd ide && npm install && npm run build`. VS Code keeps its state in the page, by its
address `127.0.0.1:47823`: a server left by an app that crashed is ended by the next start (the
running app holds `serve-web.lock` beside it; `serve-web.pid` records the server's process, boot
and start time, and nothing it does not prove is signalled). A second acpost running beside the
first gets no IDE of its own (it would share the first's folders): its IDE view says so. When
something else holds the port, VS Code starts on another one, without that state.

Beside the IDE the chat follows the agent into the editor: when a tool call that edited a file
completes, the file opens in VS Code as a diff of that edit (a preview tab, the focus left where it
was; "Follow the agent" in the IDE's bar turns it off), and a file's path in a tool row opens it
the same way. The app asks by writing a request (`IDEReveal`: the folder, the file, the edit's text
before and after) as a file in `vscode/commands`, a folder of the user's alone; the extension
(`extension.js`, `reveal.js`) watches it in the window that has that folder open. Nothing listens
on a port. Only an agent of this Mac's node has its edits shown (not a cloud sandbox's or another
node's, nor any agent's when the app is on another node through `ACPOST_NODE`: their paths are
another machine's). The diff's left side is the file as it is with the edit taken back (the new text, found
once, replaced by the old): an edit whose text the event cut, or that is no longer in the file, opens
the file without a diff. The chat's header there has "New task" and "Another chat" (the palette),
the sidebar being hidden.

A reply can carry components besides Markdown and Mermaid: a ```` ```ui ```` block holds one JSON
object `{"type": …}` (or an array of up to 8) that the chat draws in its place: `stats` tiles, a
`chart` (bar, line, area or pie, drawn as SVG in the app's colours), a `table` the person sorts and
filters, a `timeline`, a `kanban` board, a plan's `steps`, and `choices` or a `form` whose answers
go into the message being written (never sent by themselves). The node tells an agent of them once a
session (`readerNote`, `internal/node/knowledge.go`). A block is checked field by field against
limits (`frontend/src/lib/genui.ts`); one that is not valid, or still being written, is shown as
its code. Its text is only ever shown as text, and nothing in it is loaded or run.
