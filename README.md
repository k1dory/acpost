# acpost

Asynchronous messages between coding agents, from a shell.

```sh
id=$(acpost send --to agent:reviewer --task TASK-42 --body "Review PR #123")
acpost wait "$id"          # prints the reviewer's answer
```

`acpost` gives every agent an address and a mailbox. A message to an agent that speaks the
[Agent Client Protocol](https://agentclientprotocol.com) (Codex through `codex-acp`, Claude Code
through `claude-agent-acp`, OpenCode, Hermes, Gemini CLI…) is delivered as a prompt in an ACP
session; the agent's answer is the reply. Any other program — a backend, a script, a human — can
own a mailbox too and answer through the same commands or the HTTP API.

One binary does everything: the command line, the local node that runs agents, and the hub that
connects nodes on several machines. A hub is a node with a different configuration file.

![acpost: CLI and backends talk to a node; the node drives ACP agents over stdio; nodes join a hub](docs/architecture.svg)

- [Concepts](#concepts)
- [Install](#install)
- [Quick start](#quick-start)
- [Commands](#commands)
- [Configuration](#configuration)
- [HTTP API](#http-api)
- [Storage and limits](#storage-and-limits)
- [Security](#security)
- [How ACP is used](#how-acp-is-used)
- [Status](#status)

## Concepts

**Address.** `agent:<name>` is an agent on the node you talk to, or on any node joined to the
same hub. `agent:<name>@<node>` names the node explicitly. A bare `<name>` means `agent:<name>`.
Senders have addresses too: `user:<login>` for a person at a shell, `agent:<name>` for an agent
the node runs, `client:<token>` for a program authenticated by a token.

**Message.** One request and, at most, one reply.

| Field | |
|---|---|
| `id` | `msg_` + a ULID: sortable by creation time, unique across nodes |
| `from`, `to` | addresses |
| `task` | free-form label (`TASK-42`, a ticket URL). Messages with the same task to the same agent share its ACP session |
| `thread` | conversation id; a new message starts its own (`thread = id`) |
| `parent`, `depth` | the message during whose handling this one was sent, and how deep the chain is |
| `body` | text, up to `limits.max_body` (1 MiB); Markdown goes as it is |
| `files` | files attached to the message and to its reply: `name`, `type`, `size`, `reply`, and `sha256` of content or `uri` of a link. Up to `limits.max_files` (16) and `max_files_size` (16 MiB) each side |
| `meta` | string map, up to 4 KiB |
| `state` | see below |
| `reply` / `error` | the answer, or why there is none |
| `expires_at` | after it a message still queued expires (`--ttl`) |

**States.**

```
queued ──► running ──► done        reply set
   │          ├──────► failed      error set; agent crashed, timed out, refused, lease lost too often
   │          └──────► cancelled   acpost cancel
   └────────► expired              ttl passed before any agent took it
```

`done`, `failed`, `cancelled` and `expired` are final. Delivery is *at least once*: a message whose
agent dies mid-turn, or whose inbox consumer loses its lease, runs again (up to
`max_attempts`, default 3). An agent can be told a message is a retry: the prompt says so.

**Agent.** A mailbox with a driver:

- `acp` — the node starts the agent's command, speaks ACP over its stdin/stdout, and turns each
  message into a `session/prompt`. The final text of the turn is the reply.
- `inbox` — the node only stores messages. Someone else takes them (`acpost recv`, or
  `POST /v1/agents/{name}/claim`) and answers (`acpost reply`). A backend, a bot, a person.

**Node.** `acpost serve`: the process that owns the store, runs `acp` agents and serves the API on a
Unix socket (and, if configured, on TCP). The command line starts a local node on first use if
none is running (its pid is in `acpost.pid` next to the socket, its log in `acpost.log`), so for
local work there is nothing to set up but the agents. `SIGTERM` stops it cleanly.

**Hub.** A node other nodes join (`acpost serve --join https://hub:7420`). A joined node announces
its agents; a message to an agent the node does not have goes to the hub, and the hub hands it to
the node that announced that agent. A node takes messages from its hub over one long poll while idle and delivers up to 8 at
once, so one downloading large files does not hold up the rest (messages from the hub are not
ordered among themselves). Nodes connect out to the hub, so a node behind NAT works. The
sending node keeps a local copy that follows the hub's outcome, so `wait`, `show` and `list` work
alike for local and remote messages.

## Install

```sh
go install github.com/k1dory/acpost/cmd/acpost@latest
```

Pure Go, no cgo: the store is SQLite compiled to Go (`modernc.org/sqlite`). The ACP adapters are
separate:

```sh
npm i -g @agentclientprotocol/codex-acp @agentclientprotocol/claude-agent-acp
```

## Quick start

`~/.config/acpost/config.yaml`:

```yaml
agents:
  reviewer:
    driver: acp
    command: [codex-acp]
    cwd: ~/src/project
    permissions: allow
  claude:
    driver: acp
    command: [claude-agent-acp]
    cwd: ~/src/project
```

```sh
$ acpost send --to reviewer --task TASK-42 --body "Review PR #123"
msg_01JC8Z6G3QK5W9R2F0V7N4XH1M
$ acpost wait msg_01JC8Z6G3QK5W9R2F0V7N4XH1M --timeout 10m
Two problems: …
```

From inside an agent's shell, the same: a node sets `ACPOST_NODE` and a token of its own
(`ACPOST_TOKEN`) in the environment of every agent process it starts. A message sent with that
token comes `from` that agent, and its `parent` is the message the agent is working on, so Claude
can ask Codex and Codex can ask somebody else; the chain is recorded (`parent`, `depth`) and
bounded (`limits.max_depth`).

## Commands

```
acpost send    --to ADDR [--body TEXT | --body-file F | stdin] [--attach FILE]... [--task T] [--thread ID]
               [--link PATH|URL]... [--session ACP-ID] [--meta k=v]... [--ttl DUR] [--idempotency-key K]
               [--wait [--timeout DUR] [--save DIR]]
acpost wait    ID... [--timeout DUR] [--save DIR]
acpost conversations [--limit N] [--before CURSOR]   # threads, the most recently active first
acpost show    ID [--events | --follow]    # --follow: the agent's text as it writes, then the outcome
acpost list    [--to ADDR] [--from ADDR] [--task T] [--thread ID] [--state S] [--limit N] [--after CURSOR]
acpost cancel  ID
acpost recv    --as NAME [--wait DUR] [--lease DUR]
acpost reply   ID --lease L (--body TEXT | --body-file F | stdin | --fail REASON) [--attach FILE]...
acpost files   ID [NAME...] [--reply] [-o DIR | --stdout] [--force]
acpost attach  FILE... [--link]            # inside an agent's turn: files for its reply
acpost agents                              # this node's, and (joined) other nodes' through the hub
acpost sessions --agent NAME [--cwd DIR]
acpost serve   [--config FILE] [--join URL]
acpost status
acpost changes ID [--tree] [--stat]         # what its turns changed in their git repository
acpost term    [--agent A | --cwd DIR] [--id T] [--list] [--keep]   # a shell on the node's machine
acpost forge   login HOST [--kind K] | logout HOST | status | prs | checks [--ref R] | log JOB | open --title T [--base B]
               [--agent A | --dir DIR]      # pull requests and CI of a repository's forge
acpost usage   [--since 30d | --from DAY [--to DAY]] [--by agent|model|day|session|""] [--agent A]
```

**What the agent did.** A tool call is a `tool_call` event when it starts and `tool_call_update`
events as it goes (when its input becomes known, an edit, its end), each with the call as its
updates made it so far: title, kind, status, input (the command, the path; up to 2 KiB), what the
agent says it is for (`description`), locations, the tail of its output (4 KiB) and exit code,
and its edits (`path`, lines `added`/`removed`, `old`/`new` text up to 8 KiB). The node declares
the `terminal_output` extension, so codex-acp sends a command's output and exit code too. Secrets
are masked before anything is kept or relayed: values of fields named like one (`token`,
`password`, `api_key`, `authorization`…), bearer and basic credentials, `NAME_TOKEN=…`-style
assignments, `--token …`-style flags and passwords in URLs. This is masking by pattern, a defence in
depth, not a guarantee: a secret in a shape it does not know, or (in a command's output past 64 KiB,
which is cut to its tail) one split across the cut, can stay as it is.

**What a session changed.** When an ACP agent works in a git repository, the node takes the
working tree before and after each turn (`git add -A` and `write-tree` through an index of its
own: the repository's index, working tree and refs are not touched, `.gitignore` is followed; the
snapshots are trees in the repository's object store, collected by git in time) and, when they
differ, keeps the turn's files and diff (up to 1 MiB, secrets masked) and records a `changes`
event with the files and their lines, which crosses the hub like the others (the diff itself stays
on the agent's node). The repository is the agent's, and so is its configuration: git runs with
hooks and fsmonitor off, every filter driver it declares a pass-through and no textconv, in a
process group killed after 20 s; a repository with over 64 MiB or 10 000 untracked files is not
snapshot. Changes are for the node's user and its agents. `acpost changes ID` and `GET /v1/messages/{id}/changes?diff=1` give a
message's turns and each repository's diff from its first snapshot to its last; `--tree`
(`tree=1`) adds the messages its agent sent while working (subagents) on this node. Turns that run
at once in one repository each see the other's changes.

**A terminal.** `acpost term` (and `POST /v1/terminals`) starts the user's login shell in a
pseudo-terminal on the node's machine, in an agent's working directory or another; for the
node's user only (not its agents, not tokens), at most 32 at once. Its output streams as
Server-Sent Events (`GET /v1/terminals/{id}/output?from=N`: `event: data`, base64, its id the
offset after it; `event: end` with the exit code) and the last 256 KiB are kept, so a reader comes
back where it was (`--id`); input is `POST …/input` (raw bytes), size `POST …/resize`. A terminal
goes when closed, or ten minutes after its shell ends.

**Pull requests and CI.** `acpost forge` (and `/v1/forge…`) reads the forge a repository's
`origin` names (an agent's working directory, or a directory): GitHub (github.com and
Enterprise), GitLab (gitlab.com and self-managed), Forgejo and Gitea. What a self-hosted host is,
and its version, it asks the host (`/api/v1/version`, `/api/v4/version`,
`X-GitHub-Enterprise-Version`), unless `login --kind` says. It lists pull requests, CI runs of a
branch with their jobs (GitHub Actions runs, GitLab pipelines, Forgejo/Gitea commit statuses),
reads a job's log (secrets masked; Forgejo and Gitea before 1.24 give none over their API, and
the error says so) and opens a pull request from the repository's branch. Tokens are kept by
`acpost forge login HOST` (read from the terminal or stdin): in the macOS keychain (service
`acpost-forge`, given to `security` on its stdin), elsewhere in `~/.config/acpost/forge-tokens.json`
(0600). For the node's user and its agents.

**Watching an answer being written.** An ACP agent's text and thinking are kept as `text` and
`thought` events while its turn runs (gathered to a few a second, at most 4 KiB each), in order
with its tool calls and plan. `GET /v1/messages/{id}/events?follow=1` streams them as Server-Sent
Events, `acpost show ID --follow` prints them (the text on stdout, the rest on stderr), and the
reply is the turn's text as before. Through a hub too: the agent's node hands each message's
events to the hub as they are recorded (the hub keeps how far it took them, so a retry adds nothing
twice), and the sender's node copies them from the hub's stream onto its own copy, so following a
message works the same wherever its agent runs (a node restarted meanwhile gets what it missed
from the hub before the message is final there).

**What agents used and cost.** At the end of each ACP turn the node records a `usage` event: the
turn's tokens as the agent reports them (input without the cache, output, cache read and write,
thinking; ACP's `usage`, an unstable part of the protocol that claude-agent-acp and codex-acp
send), the session's cumulative cost and context (`usage_update`), and the model (the selected
option's name; for claude-agent-acp's "default" its description, such as "Opus 5.5"). The event
carries the turn's own cost, worked out where the agent runs (how much the session's cost grew
since its turn before), the turn's time and an id. It crosses the hub like the other events, and
past the event limit (a few a message), and each node counts a turn once, on the day it ran: the
agent's node, the sender's, the hub (which takes a node's usage only in the name of the agent the
message went to). A turn that failed after the agent said what it cost is counted too; one that was
cancelled is counted where the agent runs only (the hub has let the message go by then). A
session taken up in the middle (`--session`, or loaded without saying its cost) has its first turn
here counted without a cost: what the session cost before is not known. Counts are
kept 400 days, past the messages. `acpost usage` and `GET /v1/usage` sum them by agent, model, day
or session; the cache's share is cache read / (input + cache read + cache write), and for turns
whose agent gives no cost (codex-acp) the node estimates by `prices` in its config:

```yaml
prices:                              # per million tokens, by the model name usage shows
  gpt-6.1-sol: {input: 1.25, output: 10, cache_read: 0.125, currency: USD}
```

`acpost message send …` is the same as `acpost send …` (and so on for the other message commands).

`--session` puts the message into an existing ACP session of the agent instead of one acpost
manages: a Codex conversation started in an editor, say. `acpost sessions --agent codex` lists the
agent's sessions, newest first (ACP `session/list`); `--session latest` takes the newest. The node
resumes or loads the session; if the agent cannot (Codex lets one client write a conversation at a
time), the message fails with `session_busy` and says what to do.

**Files are content or links, and nothing is kept twice.** Content is stored once per SHA-256:
the same file in many messages, or handed back in a reply, takes its bytes once. A link (`--link
PATH|URL`: a file of this machine or an http(s) URL) is kept where it is; acpost stores only the
link, `files` and `--save` report it rather than copy it, and an agent gets it as the link.
A `file://` link opens only on its machine, so it does not leave the node: a message with one to
an agent on another node is refused (`--attach` sends a copy), and so is `acpost attach --link`
for a sender on another node.

**Files in an agent's answer.** The `image`, `resource` and `resource_link` blocks an ACP agent
puts in its answer become the reply's files (not text of the reply): a `resource_link` stays a
link; content the agent was given in the turn is the stored file again; embedded content whose
`file://` URI names a file on disk with the same bytes is a link to that file; anything else is
content. For a sender on another node there are no `file://` links: embedded content stays content,
and a `resource_link` to a file on disk is left out and said (the node does not read files for an
agent; `acpost attach FILE` sends a copy). Past the reply's limits the rest is left out and said
in an event; for a sender on another node, whose events are elsewhere, also in `[acpost]` lines at
the end of the reply.

`--attach` (repeatable) sends files with a message or a reply; a file goes by its base
name, and its type comes from the extension or its first bytes. An `acp` agent gets them in its
prompt the way ACP carries context: a link as the link, a text file embedded (`resource`) when
the agent takes embedded context, an image inline (`image`) when it takes images, anything else as
a `resource_link` to a copy on disk that lives until the turn ends (only these are copied); the
prompt lists the files and where each is. An agent attaches files to its answer from inside its turn with `acpost attach FILE…` (its
`ACPOST_TOKEN` says which message it is answering); an inbox consumer uses `reply --attach`.
What is embedded in a prompt is bounded (4 MiB of encoded content); past it files go as links.
The CLI checks the node's limits (`status` reports them) before it reads a file. `recv`, `show` and `wait` list the files; `acpost files ID` saves the message's, `--reply` the
reply's, and `send --wait --save DIR` (or `wait --save DIR`) saves the reply's files as it arrives. Files cross a hub with their message both ways, within each node's limits.

**Global flags** (and their variables): `--node URL` (`ACPOST_NODE`; `unix:///path` or
`http(s)://host:port`, default the local socket), `--token T` (`ACPOST_TOKEN`), `--json`
(`ACPOST_JSON=1`), `--config FILE` (`ACPOST_CONFIG`).

**Output** is made to be read by a program as much as by a person:

- `send` prints the id and nothing else. With `--wait` it waits like `wait`.
- `wait` prints the reply body as is. With several ids it waits for all and prints replies in order,
  separated by a line `--- <id>`.
- `list` prints one line per message; `--json` makes it NDJSON, one object per line, ending with
  `{"next":"<cursor>"}` when there is more.
- `--json` turns every command's output into JSON: the message record, or `{"error":{…}}` on stderr.
- `send` reads the body from stdin when neither `--body` nor `--body-file` is given and stdin is not
  a terminal.

**Errors** say what happened and what to do next, the same way for a person and for an agent:

```
$ acpost send --to codex --session latest --body "…" --wait
acpost: msg_01JC… failed [session_busy]: the session is open in another client of the agent
  hint: close it there, or send without --session to start a new conversation
```

With `--json` the same is `{"error":{"code":"session_busy","message":"…","hint":"…"}}` on stderr,
and a failed message carries `error_code`, `error` and `hint`. Codes are stable; the agent's own
text, when it adds something, is in `error` after the explanation.

| `error_code` | Meaning |
|---|---|
| `session_busy` | the session is open in another client of the agent |
| `session_not_found` | the agent has no such session (`acpost sessions`) |
| `agent_auth` | the agent needs a login or an API key |
| `agent_start` | the agent's command did not start or initialize |
| `agent_crashed` | the agent exited mid-turn (retried up to `max_attempts`) |
| `turn_timeout` | the turn ran past `turn_timeout` |
| `agent_stopped` | the agent ended the turn itself (refusal, token limit) |
| `unsupported` | the agent does not offer what was asked (e.g. `session/list`) |
| `lease_lost` | an inbox consumer or a node lost its claim too often |
| `node_restart` | the node restarted during the last allowed attempt |
| `files_refused` | a node on the way (the hub, the sender's node) refused the reply's files by its limits; the reply's text is kept |
| `agent_error` | anything else the agent reported |

**Exit codes.**

| Code | Meaning |
|---|---|
| 0 | ok (for `wait`: every message is `done`) |
| 1 | other error |
| 2 | bad usage |
| 3 | no such message or agent |
| 4 | `--timeout` reached; the message keeps going |
| 5 | the message ended `failed`, `cancelled` or `expired` |
| 6 | no node to talk to |
| 7 | refused: not allowed, a limit, a chain too deep |

`--timeout` defaults to no limit for `wait` and is advisable from an agent, whose shell tool has its
own. `--idempotency-key` makes a repeated `send` (a retry after a network error) return the first
message instead of creating another; keys are per sender and kept as long as the message.

## Configuration

YAML or JSON (by extension). Every field is optional; this is the full set with defaults.

```yaml
node:
  name: laptop                       # default: host name; how a hub names this node
  listen:
    - unix://~/.local/state/acpost/acpost.sock
    # - tcp://0.0.0.0:7420           # TCP needs tls (below) unless it is loopback
  tls: {cert: /etc/acpost/tls.crt, key: /etc/acpost/tls.key}
  join: {url: https://hub.example:7420, token_env: ACPOST_JOIN_TOKEN, ca: /etc/acpost/hub-ca.crt}

store: sqlite://~/.local/state/acpost/store.db   # or memory (tests), postgres://… (planned)

retention: 168h                      # final messages are deleted after this
limits:
  max_body: 1MiB
  max_reply: 1MiB
  max_meta: 4KiB
  max_files: 16                      # files attached to a message, and to its reply (at most 256)
  max_files_size: 16MiB              # their bytes together, each side
  max_depth: 4                       # parent chain length
  max_queued_per_agent: 1000
  max_events_per_message: 1000
  max_store: 2GiB                    # sends are refused (exit 7) above this
  max_attempts: 3

tokens:                              # TCP only; a Unix socket is guarded by file permissions
  - name: ci                         # principal: client:ci
    sha256: 6b86b273ff34fce1…        # of the token; or token_env: CI_TOKEN
    send_to: ["agent:reviewer"]      # globs; default: none
    act_as: []                       # inbox agents it may recv/reply for
  - name: laptop                     # a node that may join this one as its hub: the name is the node's
    sha256: d4735e3a265e16eee03f5…
    node: true

agents:
  reviewer:
    driver: acp                      # acp | inbox
    command: [codex-acp]             # argv; no shell
    cwd: ~/src/project
    env: {CODEX_HOME: ~/.codex-reviewer}
    auth_method: ""                  # ACP authenticate method id, if the agent needs one
    mode: ""                         # ACP session mode / config option
    model: ""
    thought_level: ""
    permissions: allow               # allow | deny: the answer to session/request_permission
    session: task                    # task | thread | message: which messages share an ACP session
    reply: last                      # last: text after the turn's last tool call; all: every text
    concurrency: 1                   # turns at once (one ACP session each)
    turn_timeout: 30m
    idle_timeout: 10m                # the agent process is stopped after this long without work
    preamble: true                   # tell the agent who wrote and how its answer is returned
    allow_from: ["*"]                # who may send to it
  backend-bot:
    driver: inbox
    lease: 5m
```

Paths starting with `~` are expanded. Durations are Go durations (`90s`, `30m`, `168h`); sizes take
`KiB`, `MiB`, `GiB`.

## HTTP API

The command line is a client of this API; a backend uses it the same way. JSON in and out; errors
are `{"error":{"code":"not_found","message":"…","hint":"…"}}` with codes `invalid`, `not_found`, `refused`,
`conflict`, `timeout`, `unavailable`.

| | |
|---|---|
| `POST /v1/messages` | send. Body: `to`, `body`, `task`, `thread`, `session`, `meta`, `ttl`, `files` (`[{"name":"a.md","data":"<base64>"}]`). Header `Idempotency-Key`. 201, or 200 for a repeat |
| `GET /v1/messages/{id}` | the message; `files` lists its files without their data |
| `GET /v1/messages/{id}/files` | the files of the message and its reply, with `data` (base64) |
| `POST /v1/attach` | `{"files":[…]}`: an agent process attaches files to the reply it is working on |
| `GET /v1/messages/{id}/wait?timeout=30s` | returns when the message is final or the timeout (max 60s) passes; look at `state` |
| `GET /v1/messages?to=&from=&task=&thread=&state=&limit=&after=` | a page (≤ 500) of messages without bodies; `next` is the cursor |
| `GET /v1/messages/{id}/events?after=` | what the agent did: its text (`text`, `thought`), tool calls, plan, permissions, notices |
| `GET /v1/messages/{id}/events?follow=1` | the same as Server-Sent Events while they are recorded, then `event: end` with the final message; `Last-Event-ID` resumes |
| `GET /v1/conversations?before=&limit=` | threads, the most recently active first: title, agents, counts by state; `next` continues (a thread active again meanwhile is at the top, not on a later page) |
| `GET /v1/messages/{id}/changes?tree=1&diff=1` | what its turns changed, per turn and per repository |
| `POST /v1/terminals` · `GET /v1/terminals` · `GET /v1/terminals/{id}/output?from=` · `POST /v1/terminals/{id}/input` · `POST /v1/terminals/{id}/resize` · `DELETE /v1/terminals/{id}` | a shell on the node's machine (the node's user) |
| `GET /v1/forge?agent=&dir=` · `GET /v1/forge/pulls?state=` · `POST /v1/forge/pulls` · `GET /v1/forge/checks?ref=` · `GET /v1/forge/log?job=` | the repository's forge: pull requests, CI, logs |
| `GET /v1/usage?from=&to=&by=&agent=` | usage summed by agent, model, day or session (the node's user) |
| `POST /v1/messages/{id}/events` | (a joined node, to its hub) `{"lease":"…","after":N,"events":[{"kind","data"}]}`: the events of a message it holds, after its event N; answers `{"relayed":M}`, how many it has taken |
| `POST /v1/messages/{id}/cancel` | |
| `POST /v1/agents/{name}/claim?wait=30s&lease=5m` | an inbox consumer takes a message (200) or none came (204) |
| `POST /v1/messages/{id}/reply` | `{"lease":"…","body":"…","files":[…]}` or `{"lease":"…","error":"…"}` |
| `POST /v1/messages/{id}/lease` | extend a claim |
| `GET /v1/agents` | agents, their drivers and queues |
| `GET /v1/agents/{name}/sessions?cwd=&cursor=` | an ACP agent's sessions, newest first |
| `GET /v1/health` | |

Who sends is decided by the node, never by the request body: a request with the token of an agent
process the node started comes from that agent (and its parent is that agent's current message);
any other request on the Unix socket comes from `user:<login>`; a TCP client is its token's
principal.

## Storage and limits

The store is one SQLite file written only by the node (a lock file stops a second node on the same
store). That is all the "database" there is: nothing to install or start.

- **Concurrent writes** go through one connection in the node, so writers queue in memory instead of
  failing with `SQLITE_BUSY`; reads use their own connections and never wait for writes (WAL).
- **Growth is bounded.** Final messages are deleted after `retention` in small batches; freed pages
  are returned to the file system (`auto_vacuum=incremental`); the WAL is checkpointed and truncated
  (`journal_size_limit`). Above `max_store` new sends are refused rather than filling the disk.
- **Limits are checked on the text**, not on its JSON: a body at `max_body` is accepted however
  much JSON escaping inflates it.
- **Reads are bounded.** Lists are pages of at most 500 messages and never carry bodies; events come
  in pages; a body or reply is never bigger than its limit. Files live in their own table and are
  read only when asked for (`acpost files`, an agent's turn), never with a message or a list. Memory does not grow with the number of
  messages stored.

The tests check each of these claims: `go test ./internal/store -run 'Concurrent|Growth|Memory'`.

## Security

- The Unix socket and the store live in a directory only the user can open (`0700`). A node
  refuses to start on a store whose directory other users can open, and makes the store's files
  `0600`.
- TCP refuses to start on a non-loopback address without TLS. Every TCP request needs a token; a
  token can send only where `send_to` allows and act only as the agents in `act_as`.
- An `acp` agent runs as the node's user, in its `cwd`, with the node's environment (its API keys,
  `PATH`, `HOME`) plus the agent's `env` and the node's own `ACPOST_NODE` and `ACPOST_TOKEN`.
  Give an agent its own `HOME`/`CODEX_HOME` through `env` to keep it apart. `permissions: allow` lets it do whatever its
  own sandbox allows: give such an agent to senders you trust (`allow_from`).
- A message from another node is a prompt for your agent: joining a hub means trusting everyone who
  can send through it. The hub only routes to the agents each node announces, and a node announces
  only agents whose `allow_from` admits remote senders (`*`, or a pattern with `@`). Senders from
  other nodes always carry their node (`user:lev@laptop`), so `allow_from: [user:lev]` admits only
  the local `lev`. A node applies its own limits (`max_body`, `max_meta`, `max_depth`,
  `max_queued_per_agent`) to what the hub delivers and fails what does not fit.
- On a hub, a joined node's token is named after the node and carries `node: true`; the node may
  then send only as its own addresses (`…@<node>`) and take only messages for agents it announced.
- An agent's sessions are its conversations with whoever started them: only the node's own user and
  agents (within `allow_from`) may list them or send into one (`--session`); token clients may not.
  Meta keys starting with `acp_` are acpost's own and refused from senders.
- Bodies and files are stored in clear text in the store. Do not send secrets. An `acp` agent's
  copies of attached files are in `<store>.turns` (next to the store file, private), each file in
  a directory of its own, removed when its turn ends and, after a crash, when the node starts again.
- Files are checked by every node they pass: names are base names (no `/`, `..`), counts and sizes
  are within that node's limits (checked on the listing before anything is downloaded), and a hub's
  response is read only up to what the node would accept.
  `acpost files` and `--save` write only inside the directory given; `--force` replaces a file (a
  symlink too) without writing through it. Only an agent process in a turn may `attach`, and only to
  the attempt it is running.

## How ACP is used

ACP is a client↔agent protocol: an editor drives an agent over stdio (JSON-RPC 2.0). It has no
notion of addresses or mailboxes, so acpost puts its own envelope around it: the node is an ACP
**client** of each `acp` agent, the messages are acpost's.

| acpost | ACP |
|---|---|
| agent process started | `initialize` (no client `fs`/`terminal`: agents use their own tools), `authenticate` when `auth_method` is set |
| first message of a session key | `session/new` (cwd) and the configured mode, model, thought level |
| next message with that key | `session/prompt` in the same session; after a restart of the agent `session/resume` or `session/load` when the agent offers them |
| attached files | `resource` (text, with `embeddedContext`), `image` (with `image`), else `resource_link` to a copy on disk |
| reply | `agent_message_chunk` text of the turn; its `image`/`resource`/`resource_link` blocks and files from `acpost attach` are the reply's files; `stopReason` other than `end_turn` is a failure |
| permission request | answered by `permissions`, recorded as an event |
| `acpost cancel` | `session/cancel` |
| tool calls, plans, notices | events of the message (`show --events`) |

The ACP client layer is [`github.com/k1dory/acpx`](https://github.com/k1dory/acpx) over
`github.com/coder/acp-go-sdk`.

ACP over the network is still being specified (the Streamable HTTP / WebSocket transport RFD, Active
since July 2026). Node-to-node and client-to-node traffic is therefore acpost's own HTTP API, not
ACP. Exposing a node's agents to remote ACP clients (an editor attaching to `agent:reviewer@server`)
fits that RFD and can come once it settles.

## Status

| | |
|---|---|
| Local node, CLI, HTTP API, SQLite store, `acp` and `inbox` drivers | v0.1 |
| `--join` and hub routing | v0.1 |
| Postgres store for a hub | planned |
| ACP transport for remote ACP clients | after the RFD |

## License

MIT
