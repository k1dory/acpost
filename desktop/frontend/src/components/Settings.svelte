<script lang="ts">
  import { app } from "../lib/state.svelte";
  import { Node, errText } from "../lib/api";
  import Icon from "./Icon.svelte";
  import Dropdown from "./Dropdown.svelte";

  type Account = { host: string; kind?: string; version?: string; authed: boolean; login?: string; error?: string };
  type MCP = { name: string; kind: "stdio" | "http"; command?: string; args?: string[]; url?: string; env?: string[]; headers?: string[]; agents?: string[]; enabled: boolean };

  let section = $state<"git" | "mcp" | "playbooks" | "schedules" | "agents" | "general">("git");
  type Sched = { id?: string; name: string; cron: string; agent: string; body: string; cwd?: string; model?: string; effort?: string; enabled: boolean; next_run?: string; last_run?: string; last_msg?: string; last_err?: string };
  let scheds = $state<Sched[] | null>(null);
  let sc = $state<Sched | null>(null);
  const presets = [["Every hour", "@hourly"], ["Every day at 9", "0 9 * * *"], ["Weekdays at 9", "0 9 * * 1-5"], ["Every Monday", "0 9 * * 1"]];
  async function loadScheds() {
    try { scheds = ((await Node.Schedules()) ?? []) as any; } catch (e) { err = errText(e); scheds = []; }
  }
  async function saveSched(s: Sched) {
    err = ""; busy = true;
    try { await Node.PutSchedule(s as any); sc = null; await loadScheds(); } catch (e) { err = errText(e); }
    busy = false;
  }
  async function runSched(id: string) {
    err = "";
    try { await Node.RunSchedule(id); await Promise.all([loadScheds(), app.loadChats()]); } catch (e) { err = errText(e); }
  }
  async function deleteSched(id: string) {
    try { await Node.DeleteSchedule(id); sc = null; await loadScheds(); } catch (e) { err = errText(e); }
  }
  async function pickSchedDir() {
    if (!sc) return;
    const d = await Node.PickDirectory(sc.cwd || app.home).catch(() => "");
    if (d) sc.cwd = d;
  }
  function when(t?: string) {
    if (!t) return "";
    const d = new Date(t);
    return d.toLocaleString(undefined, { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
  }
  let pb = $state<{ isNew: boolean; name: string; body: string } | null>(null);
  async function savePlaybook() {
    if (!pb) return;
    err = ""; busy = true;
    try { await Node.PutPlaybook(pb.name.trim(), pb.body); pb = null; await app.loadPlaybooks(); } catch (e) { err = errText(e); }
    busy = false;
  }
  async function deletePlaybook(name: string) {
    try { await Node.DeletePlaybook(name); pb = null; await app.loadPlaybooks(); } catch (e) { err = errText(e); }
  }
  let accounts = $state<Account[] | null>(null);
  let servers = $state<MCP[] | null>(null);
  let err = $state("");
  let busy = $state(false);

  // Git: a new account.
  let host = $state("github.com");
  let kind = $state("");
  let token = $state("");

  // MCP: the server being edited (null: none).
  type Pair = { name: string; value: string; kept: boolean };
  let edit = $state<{ isNew: boolean; name: string; kind: "stdio" | "http"; command: string; args: string; url: string; env: Pair[]; headers: Pair[]; agents: string[]; enabled: boolean } | null>(null);

  async function loadAccounts() {
    try { accounts = ((await Node.ForgeAccounts()) ?? []) as any; } catch (e) { err = errText(e); accounts = []; }
  }
  async function loadServers() {
    try { servers = ((await Node.MCPServers()) ?? []) as any; } catch (e) { err = errText(e); servers = []; }
  }
  $effect(() => { if (section === "git" && accounts === null) loadAccounts(); if (section === "mcp" && servers === null) loadServers(); if (section === "schedules" && scheds === null) loadScheds(); });

  async function connect() {
    err = ""; busy = true;
    try {
      await Node.ForgeLogin(host.trim(), kind, token.trim());
      token = "";
      await loadAccounts();
    } catch (e) { err = errText(e); }
    busy = false;
  }
  async function disconnect(h: string) {
    try { await Node.ForgeLogout(h); await loadAccounts(); } catch (e) { err = errText(e); }
  }

  function newServer() {
    edit = { isNew: true, name: "", kind: "stdio", command: "", args: "", url: "", env: [], headers: [], agents: [], enabled: true };
  }
  function editServer(m: MCP) {
    edit = {
      isNew: false, name: m.name, kind: m.kind, command: m.command ?? "", args: (m.args ?? []).join("\n"), url: m.url ?? "",
      env: (m.env ?? []).map(n => ({ name: n, value: "", kept: true })), headers: (m.headers ?? []).map(n => ({ name: n, value: "", kept: true })),
      agents: m.agents ?? [], enabled: m.enabled,
    };
  }
  async function save() {
    if (!edit) return;
    err = ""; busy = true;
    const e = edit;
    const secrets: Record<string, string> = {};
    for (const p of e.env) if (p.name && (!p.kept || p.value)) secrets["env/" + p.name] = p.value;
    for (const p of e.headers) if (p.name && (!p.kept || p.value)) secrets["header/" + p.name] = p.value;
    const m: MCP = {
      name: e.name.trim(), kind: e.kind, command: e.command.trim(), url: e.url.trim(), enabled: e.enabled, agents: e.agents,
      args: e.args.split("\n").map(s => s.trim()).filter(Boolean),
      env: e.env.map(p => p.name.trim()).filter(Boolean), headers: e.headers.map(p => p.name.trim()).filter(Boolean),
    };
    try { await Node.PutMCPServer(m as any, secrets); edit = null; await loadServers(); } catch (x) { err = errText(x); }
    busy = false;
  }
  async function toggle(m: MCP) {
    try { await Node.PutMCPServer({ ...m, enabled: !m.enabled } as any, {}); await loadServers(); } catch (e) { err = errText(e); }
  }
  async function remove(name: string) {
    try { await Node.DeleteMCPServer(name); edit = null; await loadServers(); } catch (e) { err = errText(e); }
  }
  function agentName(a: string) {
    const n = a.replace(/^agent:/, "");
    return n === "claude" ? "Claude Code" : n === "codex" ? "Codex" : n;
  }
  let notify = $state(true);
  try { notify = localStorage.getItem("notify") !== "0"; } catch {}
  function setNotify(v: boolean) { notify = v; try { localStorage.setItem("notify", v ? "1" : "0"); } catch {} }
</script>

<section class="set">
  <header class="drag"><h1>Settings</h1></header>
  <div class="cols">
    <nav class="nav">
      {#each [["git", "Git accounts", "pr"], ["mcp", "MCP servers", "term"], ["playbooks", "Playbooks", "book"], ["schedules", "Schedules", "clock"], ["agents", "Agents", "bot"], ["general", "General", "sun"]] as [k, l, ic]}
        <button class:on={section === k} onclick={() => { section = k as any; err = ""; }}><Icon name={ic} size={14} /> {l}</button>
      {/each}
    </nav>

    <div class="body scroll">
      {#if err}<div class="err selectable">{err}</div>{/if}

      {#if section === "git"}
        <h2>Git accounts</h2>
        <p class="muted">Pull requests, CI and merges of your agents' repositories. The token stays in the macOS keychain.</p>
        <div class="card panel"><div class="inner">
          {#if accounts === null}<div class="row"><span class="sk" style="width:40%;height:12px"></span></div>
          {:else}
            {#each accounts as a (a.host)}
              <div class="row">
                <Icon name="pr" size={14} />
                <div class="t"><b>{a.host}</b><span class="muted small">{a.kind ?? "?"}{a.version ? " " + a.version : ""}{a.login ? " · " + a.login : ""}</span>
                  {#if a.error}<span class="bad small">{a.error}</span>{/if}</div>
                {#if a.login}<span class="chip good">connected</span>{:else}<span class="chip warn">{a.authed ? "token refused" : "no token"}</span>{/if}
                <button class="btn ghost sm danger" onclick={() => disconnect(a.host)}>Disconnect</button>
              </div>
            {:else}<div class="row muted">No accounts yet</div>{/each}
          {/if}
        </div></div>
        <div class="form card">
          <div class="fh">Connect an account</div>
          <div class="frow">
            <input class="input" placeholder="Host: github.com, gitlab.example.com" bind:value={host} />
            <Dropdown bind:value={kind} items={[{ value: "", label: "Detect" }, { value: "github", label: "GitHub" }, { value: "gitlab", label: "GitLab" }, { value: "forgejo", label: "Forgejo / Gitea" }]} />
          </div>
          <input class="input" type="password" placeholder="Personal access token" bind:value={token} autocomplete="off" />
          <div class="acts"><button class="btn primary" disabled={busy || !host.trim() || !token.trim()} onclick={connect}>{busy ? "Checking…" : "Connect"}</button></div>
        </div>

      {:else if section === "mcp"}
        <h2>MCP servers</h2>
        <p class="muted">Every session of the agents gets these. Values of variables and headers stay in the macOS keychain.</p>
        {#if !edit}
          <div class="card panel"><div class="inner">
            {#if servers === null}<div class="row"><span class="sk" style="width:40%;height:12px"></span></div>
            {:else}
              {#each servers as m (m.name)}
                <div class="row">
                  <button class="sw" class:on={m.enabled} onclick={() => toggle(m)} title={m.enabled ? "On" : "Off"}><span></span></button>
                  <div class="t"><b>{m.name}</b><span class="muted small mono">{m.kind === "http" ? m.url : [m.command, ...(m.args ?? [])].join(" ")}</span></div>
                  <span class="chip">{m.agents?.length ? m.agents.map(agentName).join(", ") : "all agents"}</span>
                  <button class="btn ghost sm" onclick={() => editServer(m)}>Edit</button>
                </div>
              {:else}<div class="row muted">No MCP servers yet</div>{/each}
            {/if}
          </div></div>
          <div class="acts left"><button class="btn primary" onclick={newServer}><Icon name="plus" size={13} /> Add a server</button></div>
        {:else}
          {@const e = edit}
          <div class="form card">
            <div class="fh">{e.isNew ? "New MCP server" : e.name}</div>
            <div class="frow">
              {#if e.isNew}<input class="input" placeholder="Name: wiki, github, postgres" bind:value={e.name} />{/if}
              <div class="seg"><button class:on={e.kind === "stdio"} onclick={() => (e.kind = "stdio")}>Command</button><button class:on={e.kind === "http"} onclick={() => (e.kind = "http")}>HTTP</button></div>
            </div>
            {#if e.kind === "stdio"}
              <input class="input mono" placeholder="Command: npx, uvx, /usr/local/bin/server" bind:value={e.command} />
              <textarea class="input mono" rows="3" placeholder="Arguments, one per line" bind:value={e.args}></textarea>
            {:else}
              <input class="input mono" placeholder="URL: https://mcp.example.com/mcp" bind:value={e.url} />
            {/if}
            {#each [["env", e.kind === "stdio" ? "Environment" : null], ["headers", e.kind === "http" ? "Headers" : null]] as [k, label]}
              {#if label}
                {@const list = k === "env" ? e.env : e.headers}
                <div class="lbl">{label}</div>
                {#each list as p, i}
                  <div class="frow">
                    <input class="input mono" placeholder="NAME" bind:value={p.name} disabled={p.kept} />
                    <input class="input" type="password" placeholder={p.kept ? "kept — type to replace" : "value"} bind:value={p.value} autocomplete="off" />
                    <button class="btn ghost sm" onclick={() => { if (k === "env") e.env = e.env.filter((_, j) => j !== i); else e.headers = e.headers.filter((_, j) => j !== i); }}><Icon name="x" size={12} /></button>
                  </div>
                {/each}
                <button class="btn ghost sm addp" onclick={() => { const p = { name: "", value: "", kept: false }; if (k === "env") e.env = [...e.env, p]; else e.headers = [...e.headers, p]; }}><Icon name="plus" size={12} /> {k === "env" ? "Variable" : "Header"}</button>
              {/if}
            {/each}
            <div class="lbl">Agents</div>
            <div class="frow wrap">
              {#each app.localAgents() as a (a.address)}
                {@const name = a.address.replace(/^agent:/, "")}
                <button class="chip pickc" class:on={e.agents.includes(name)} onclick={() => (e.agents = e.agents.includes(name) ? e.agents.filter(x => x !== name) : [...e.agents, name])}>{agentName(a.address)}</button>
              {/each}
              <span class="muted small">{e.agents.length ? "" : "none chosen: all agents"}</span>
            </div>
            <div class="acts">
              {#if !e.isNew}<button class="btn ghost danger" onclick={() => remove(e.name)}>Delete</button>{/if}
              <span class="sp"></span>
              <button class="btn" onclick={() => (edit = null)}>Cancel</button>
              <button class="btn primary" disabled={busy || !e.name.trim()} onclick={save}>Save</button>
            </div>
          </div>
        {/if}

      {:else if section === "playbooks"}
        <h2>Playbooks</h2>
        <p class="muted">Prompts for tasks you give again. Type / and the name in a new task.</p>
        {#if !pb}
          <div class="card panel"><div class="inner">
            {#each app.playbooks as p (p.name)}
              <div class="row">
                <Icon name="book" size={14} />
                <div class="t"><b class="mono">/{p.name}</b><span class="muted small one">{p.body.split("\n")[0]}</span></div>
                <button class="btn ghost sm" onclick={() => (pb = { isNew: false, name: p.name, body: p.body })}>Edit</button>
              </div>
            {:else}<div class="row muted">No playbooks yet</div>{/each}
          </div></div>
          <div class="acts left"><button class="btn primary" onclick={() => (pb = { isNew: true, name: "", body: "" })}><Icon name="plus" size={13} /> New playbook</button></div>
        {:else}
          <div class="form card">
            {#if pb.isNew}<input class="input mono" placeholder="Name: review, release-notes" bind:value={pb.name} />{:else}<div class="fh mono">/{pb.name}</div>{/if}
            <textarea class="input" rows="12" placeholder="What the agent should do, step by step" bind:value={pb.body}></textarea>
            <div class="acts">
              {#if !pb.isNew}<button class="btn ghost danger" onclick={() => deletePlaybook(pb!.name)}>Delete</button>{/if}
              <span class="sp"></span>
              <button class="btn" onclick={() => (pb = null)}>Cancel</button>
              <button class="btn primary" disabled={busy || !pb.name.trim() || !pb.body.trim()} onclick={savePlaybook}>Save</button>
            </div>
          </div>
        {/if}

      {:else if section === "schedules"}
        <h2>Schedules</h2>
        <p class="muted">Tasks the node sends on a schedule. Each run is a new chat.</p>
        {#if !sc}
          <div class="card panel"><div class="inner">
            {#each scheds ?? [] as s (s.id)}
              <div class="row">
                <Icon name="clock" size={14} />
                <div class="t">
                  <b>{s.name}</b>
                  <span class="muted small one">{agentName(s.agent)} · <span class="mono">{s.cron}</span>{#if s.enabled && s.next_run} · next {when(s.next_run)}{/if}{#if s.last_run} · last {when(s.last_run)}{/if}</span>
                  {#if s.last_err}<span class="small bad one">{s.last_err}</span>{/if}
                </div>
                <button class="btn ghost sm" onclick={() => runSched(s.id!)}>Run now</button>
                <button class="btn ghost sm" onclick={() => (sc = { ...s })}>Edit</button>
                <button class="sw" class:on={s.enabled} onclick={() => saveSched({ ...s, enabled: !s.enabled })} aria-label="Enabled"><span></span></button>
              </div>
            {:else}<div class="row muted">{scheds === null ? "Loading…" : "No schedules yet"}</div>{/each}
          </div></div>
          <div class="acts left"><button class="btn primary" onclick={() => (sc = { name: "", cron: "0 9 * * 1-5", agent: app.localAgents()[0]?.address ?? "", body: "", cwd: app.home, enabled: true })}><Icon name="plus" size={13} /> New schedule</button></div>
        {:else}
          <div class="form card">
            <input class="input" placeholder="Name: morning review" bind:value={sc.name} />
            <div class="lbl">When</div>
            <div class="frow wrap">
              {#each presets as [l, c]}<button class="chip pickc" class:on={sc.cron === c} onclick={() => (sc!.cron = c)}>{l}</button>{/each}
            </div>
            <input class="input mono" placeholder="cron: min hour day month weekday" bind:value={sc.cron} />
            <div class="lbl">Agent and directory</div>
            <div class="frow">
              <Dropdown bind:value={sc.agent} icon="bot" items={app.localAgents().map(a => ({ value: a.address, label: agentName(a.address) }))} />
              <button class="chip pickc" onclick={pickSchedDir} title={sc.cwd}><Icon name="file" size={12} /> {sc.cwd && app.home && sc.cwd.startsWith(app.home) ? "~" + sc.cwd.slice(app.home.length) : sc.cwd || "~"}</button>
            </div>
            <div class="lbl">Task</div>
            {#if app.playbooks.length}
              <div class="frow wrap">
                {#each app.playbooks as p (p.name)}<button class="chip pickc" class:on={sc.body === p.body} onclick={() => (sc!.body = p.body)}>/{p.name}</button>{/each}
              </div>
            {/if}
            <textarea class="input" rows="8" placeholder="What the agent should do" bind:value={sc.body}></textarea>
            <div class="acts">
              {#if sc.id}<button class="btn ghost danger" onclick={() => deleteSched(sc!.id!)}>Delete</button>{/if}
              <span class="sp"></span>
              <button class="btn" onclick={() => (sc = null)}>Cancel</button>
              <button class="btn primary" disabled={busy || !sc.name.trim() || !sc.cron.trim() || !sc.agent || !sc.body.trim()} onclick={() => saveSched(sc!)}>Save</button>
            </div>
          </div>
        {/if}

      {:else if section === "agents"}
        <h2>Agents</h2>
        <p class="muted">The agents of this Mac (in ~/.config/acpost/config.yaml), of other nodes and of the cloud.</p>
        <div class="card panel"><div class="inner">
          {#each app.agents as a (a.address)}
            <div class="row">
              <Icon name={a.driver === "sandbox" ? "cloud" : "bot"} size={14} />
              <div class="t"><b>{agentName(a.address)}</b><span class="muted small">{a.driver}{a.node ? " · " + a.node : ""}</span></div>
              {#if a.running}<span class="chip">running {a.running}</span>{/if}
              {#if a.queued}<span class="chip warn">{a.queued} queued</span>{/if}
            </div>
          {/each}
          <div class="row">
            <Icon name="cloud" size={14} />
            <div class="t"><b>Cloud</b><span class="muted small">{app.cloudAgents.length ? app.cloudAgents.length + " agents in the catalogue" : "not configured"}</span></div>
          </div>
        </div></div>

      {:else}
        <h2>General</h2>
        <div class="card panel"><div class="inner">
          <div class="row">
            <div class="t"><b>Notifications</b><span class="muted small">When a turn ends or an agent waits for you, and the app is in the background</span></div>
            <button class="sw" class:on={notify} onclick={() => setNotify(!notify)} aria-label="Notifications"><span></span></button>
          </div>
          <div class="row">
            <div class="t"><b>Appearance</b></div>
            <div class="seg">
              <button class:on={app.theme === "light"} onclick={() => app.setTheme("light")}>Light</button>
              <button class:on={app.theme === "dark"} onclick={() => app.setTheme("dark")}>Dark</button>
              <button class:on={app.theme === "system"} onclick={() => app.setTheme("system")}>Auto</button>
            </div>
          </div>
          <div class="row">
            <div class="t"><b>Node</b><span class="muted small mono">{app.conn?.url}</span></div>
            <span class="chip" class:good={app.conn?.ok}>{app.conn?.node}</span>
          </div>
        </div></div>
      {/if}
    </div>
  </div>
</section>

<style>
  .set { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--page); }
  header { height: 54px; display: flex; align-items: center; padding: 0 28px; border-bottom: 1px solid var(--line-soft); flex: none; }
  h1 { font-size: 15px; margin: 0; }
  h2 { font-size: 15px; margin: 0 0 4px; }
  .cols { flex: 1; min-height: 0; display: flex; }
  .nav { width: 200px; flex: none; padding: 16px 10px; display: grid; align-content: start; gap: 2px; border-right: 1px solid var(--line-soft); }
  .nav button { display: flex; gap: 8px; align-items: center; border: 0; background: transparent; padding: 8px 10px; border-radius: 10px; text-align: left; font-weight: 500; color: var(--ink-2); }
  .nav button:hover { background: var(--hover); }
  .nav button.on { background: var(--sel); color: var(--ink); }
  .body { flex: 1; padding: 24px 36px 40px; max-width: 760px; display: grid; align-content: start; gap: 12px; }
  .row { display: flex; align-items: center; gap: 12px; padding: 0 14px; min-height: 52px; border-bottom: 1px solid var(--line-soft); }
  .row:last-child { border-bottom: 0; }
  .t { flex: 1; min-width: 0; display: grid; }
  .small { font-size: 11.5px; }
  .one { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .bad { color: var(--crit); }
  .form { padding: 16px; display: grid; gap: 10px; }
  .fh { font-weight: 600; }
  .frow { display: flex; gap: 8px; align-items: center; }
  .frow.wrap { flex-wrap: wrap; }
  .frow .input { flex: 1; }
  .input { height: 32px; font-size: 13px; }
  textarea.input { height: auto; resize: vertical; }
  .lbl { font-size: 11px; font-weight: 600; color: var(--muted); text-transform: uppercase; margin-top: 4px; }
  .addp { justify-self: start; }
  .acts { display: flex; gap: 8px; justify-content: flex-end; align-items: center; }
  .acts.left { justify-content: flex-start; }
  .sp { flex: 1; }
  .err { padding: 10px 12px; border-radius: 12px; background: var(--crit-bg); color: var(--crit); font-size: 12.5px; }
  .pickc { cursor: default; height: 26px; }
  .pickc.on { background: var(--accent-bg); color: var(--accent); border-color: transparent; }
  .sw { width: 34px; height: 20px; border-radius: 10px; border: 0; background: var(--track); position: relative; flex: none; }
  .sw span { position: absolute; top: 2px; left: 2px; width: 16px; height: 16px; border-radius: 50%; background: var(--card); box-shadow: 0 1px 2px rgba(0,0,0,.2); transition: left .15s; }
  .sw.on { background: var(--good); }
  .sw.on span { left: 16px; }
</style>
