<script lang="ts">
  import { agentLabel, tilde as tildeOf } from "../lib/names";
  import { mcpRequest, type MCP, type MCPEdit } from "../lib/mcp";
  import { newScheduleDraft } from "../lib/records";
  import { t, i18n, type Lang } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { Node, errText, errInfo } from "../lib/api";
  import Icon from "./Icon.svelte";
  import Dropdown from "./Dropdown.svelte";
  import Cloud from "./Cloud.svelte";

  type Account = { host: string; kind?: string; version?: string; authed: boolean; login?: string; error?: string };

  let section = $state<"git" | "mcp" | "playbooks" | "knowledge" | "schedules" | "agents" | "cloud" | "general">((app.settingsAt || "git") as any);
  app.settingsAt = "";
  type Note = { name: string; body: string; scope?: string; enabled: boolean; isNew?: boolean };
  let notes = $state<Note[] | null>(null);
  let note = $state<Note | null>(null);
  async function loadNotes() {
    try { notes = ((await Node.Knowledge()) ?? []) as any; } catch (e) { err = errText(e); notes = []; }
  }
  async function saveNote(k: Note) {
    err = ""; busy = true;
    try { await Node.PutNote({ name: k.name.trim(), body: k.body, scope: k.scope ?? "", enabled: k.enabled } as any); note = null; await loadNotes(); } catch (e) { err = errText(e); }
    busy = false;
  }
  async function deleteNote(name: string) {
    try { await Node.DeleteNote(name); note = null; await loadNotes(); } catch (e) { err = errText(e); }
  }
  async function pickNoteDir() {
    if (!note) return;
    const d = await Node.PickDirectory(note.scope || app.home).catch(() => "");
    if (d) note.scope = d;
  }
  const tilde = (d?: string) => tildeOf(d, app.home);
  type Sched = { id?: string; isNew?: boolean; name: string; cron: string; agent: string; body: string; cwd?: string; model?: string; effort?: string; enabled: boolean; next_run?: string; last_run?: string; last_msg?: string; last_err?: string };
  let scheds = $state<Sched[] | null>(null);
  let sc = $state<Sched | null>(null);
  const presets = [["Every hour", "@hourly"], ["Every day at 9", "0 9 * * *"], ["Weekdays at 9", "0 9 * * 1-5"], ["Every Monday", "0 9 * * 1"]];
  async function loadScheds() {
    try { scheds = ((await Node.Schedules()) ?? []) as any; } catch (e) { err = errText(e); scheds = []; }
  }
  async function saveSched(s: Sched) {
    err = ""; busy = true;
    const { isNew: _, ...put } = s;
    try { await Node.PutSchedule(put as any); sc = null; await loadScheds(); } catch (e) { err = errText(e); }
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
  function when(at?: string) {
    if (!at) return "";
    const d = new Date(at);
    return d.toLocaleString(i18n.lang === "ru" ? "ru-RU" : undefined, { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
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
  let edit = $state<MCPEdit | null>(null);

  async function loadAccounts() {
    try { accounts = ((await Node.ForgeAccounts()) ?? []) as any; } catch (e) { err = errText(e); accounts = []; }
  }
  async function loadServers() {
    try { servers = ((await Node.MCPServers()) ?? []) as any; } catch (e) { err = errText(e); servers = []; }
  }
  $effect(() => { if (section === "git" && accounts === null) loadAccounts(); if (section === "mcp" && servers === null) loadServers(); if (section === "schedules" && scheds === null) loadScheds(); if (section === "knowledge" && notes === null) loadNotes(); });

  async function connect() {
    err = ""; busy = true;
    try {
      await Node.ForgeLogin(host.trim(), kind, token.trim());
      token = "";
      await loadAccounts();
    } catch (e) { err = errText(e, errInfo(e).code === "refused" ? "The token was not accepted" : errInfo(e).code === "invalid" ? "Could not connect the account" : undefined); }
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
    const r = mcpRequest(edit);
    if ("error" in r) { err = t(r.error, { name: r.name ?? "" }); busy = false; return; }
    try { await Node.PutMCPServer(r.server as any, r.secrets); edit = null; await loadServers(); } catch (x) { err = errText(x); }
    busy = false;
  }
  async function toggle(m: MCP) {
    try { await Node.PutMCPServer({ ...m, enabled: !m.enabled } as any, {}); await loadServers(); } catch (e) { err = errText(e); }
  }
  async function remove(name: string) {
    try { await Node.DeleteMCPServer(name); edit = null; await loadServers(); } catch (e) { err = errText(e); }
  }
  const agentName = agentLabel;
  let notify = $state(true);
  try { notify = localStorage.getItem("notify") !== "0"; } catch {}
  function setNotify(v: boolean) { notify = v; try { localStorage.setItem("notify", v ? "1" : "0"); } catch {} }
</script>

<section class="set">
  <header class="drag"><h1>{t("Settings")}</h1></header>
  <div class="cols">
    <nav class="nav">
      {#each [["git", "Git accounts", "pr"], ["mcp", "MCP servers", "term"], ["playbooks", "Playbooks", "book"], ["knowledge", "Knowledge", "brain"], ["schedules", "Schedules", "clock"], ["agents", "Agents", "bot"], ["cloud", "Cloud", "cloud"], ["general", "General", "sun"]] as [k, l, ic]}
        <button class:on={section === k} onclick={() => { section = k as any; err = ""; }}><Icon name={ic} size={14} /> {t(l)}</button>
      {/each}
    </nav>

    <div class="body scroll">
      {#if err}<div class="err selectable">{err}</div>{/if}

      {#if section === "git"}
        <h2>{t("Git accounts")}</h2>
        <p class="muted">{t("Pull requests, CI and merges of your agents' repositories. The token stays in the macOS keychain.")}</p>
        <div class="card panel"><div class="inner">
          {#if accounts === null}<div class="row"><span class="sk" style="width:40%;height:12px"></span></div>
          {:else}
            {#each accounts as a (a.host)}
              <div class="row">
                <Icon name="pr" size={14} />
                <div class="t"><b>{a.host}</b><span class="muted small">{a.kind ?? "?"}{a.version ? " " + a.version : ""}{a.login ? " · " + a.login : ""}</span>
                  {#if a.error}<span class="bad small">{a.error}</span>{/if}</div>
                {#if a.login}<span class="chip good">{t("connected")}</span>{:else}<span class="chip warn">{a.authed ? t("token refused") : t("no token")}</span>{/if}
                <button class="btn ghost sm danger" onclick={() => disconnect(a.host)}>{t("Disconnect")}</button>
              </div>
            {:else}<div class="row muted">{t("No accounts yet")}</div>{/each}
          {/if}
        </div></div>
        <div class="form card">
          <div class="fh">{t("Connect an account")}</div>
          <div class="frow">
            <input class="input" placeholder={t("Host: github.com, gitlab.example.com")} bind:value={host} />
            <Dropdown bind:value={kind} items={[{ value: "", label: t("Detect") }, { value: "github", label: "GitHub" }, { value: "gitlab", label: "GitLab" }, { value: "forgejo", label: "Forgejo / Gitea" }]} />
          </div>
          <input class="input" type="password" placeholder={t("Personal access token")} bind:value={token} autocomplete="off" />
          <div class="acts"><button class="btn primary" disabled={busy || !host.trim() || !token.trim()} onclick={connect}>{busy ? t("Checking…") : t("Connect")}</button></div>
        </div>

      {:else if section === "mcp"}
        <h2>{t("MCP servers")}</h2>
        <p class="muted">{t("Every session of the agents gets these. Values of variables and headers stay in the macOS keychain.")}</p>
        {#if !edit}
          <div class="card panel"><div class="inner">
            {#if servers === null}<div class="row"><span class="sk" style="width:40%;height:12px"></span></div>
            {:else}
              {#each servers as m (m.name)}
                <div class="row">
                  <button class="sw" class:on={m.enabled} onclick={() => toggle(m)} title={m.enabled ? t("On") : t("Off")}><span></span></button>
                  <div class="t"><b>{m.name}</b><span class="muted small mono">{m.kind === "http" ? m.url : [m.command, ...(m.args ?? [])].join(" ")}</span></div>
                  <span class="chip">{m.agents?.length ? m.agents.map(agentName).join(", ") : t("all agents")}</span>
                  <button class="btn ghost sm" onclick={() => editServer(m)}>{t("Edit")}</button>
                </div>
              {:else}<div class="row muted">{t("No MCP servers yet")}</div>{/each}
            {/if}
          </div></div>
          <div class="acts left"><button class="btn primary" onclick={newServer}><Icon name="plus" size={13} /> {t("Add a server")}</button></div>
        {:else}
          {@const e = edit}
          <div class="form card">
            <div class="fh">{e.isNew ? t("New MCP server") : e.name}</div>
            <div class="frow">
              {#if e.isNew}<input class="input" placeholder={t("Name: wiki, github, postgres")} bind:value={e.name} />{/if}
              <div class="seg"><button class:on={e.kind === "stdio"} onclick={() => (e.kind = "stdio")}>{t("Command")}</button><button class:on={e.kind === "http"} onclick={() => (e.kind = "http")}>{t("HTTP")}</button></div>
            </div>
            {#if e.kind === "stdio"}
              <input class="input mono" placeholder={t("Command: npx, uvx, /usr/local/bin/server")} bind:value={e.command} />
              <textarea class="input mono" rows="3" placeholder={t("Arguments, one per line")} bind:value={e.args}></textarea>
            {:else}
              <input class="input mono" placeholder={t("URL: https://mcp.example.com/mcp")} bind:value={e.url} />
            {/if}
            {#each [["env", e.kind === "stdio" ? "Environment" : null], ["headers", e.kind === "http" ? "Headers" : null]] as [k, label]}
              {#if label}
                {@const list = k === "env" ? e.env : e.headers}
                <div class="lbl">{t(label)}</div>
                {#each list as p, i}
                  <div class="frow">
                    <input class="input mono" placeholder={t("NAME")} bind:value={p.name} disabled={p.kept} />
                    <input class="input" type="password" placeholder={p.kept ? t("kept — type to replace") : t("value")} bind:value={p.value} autocomplete="off" />
                    <button class="btn ghost sm" onclick={() => { if (k === "env") e.env = e.env.filter((_, j) => j !== i); else e.headers = e.headers.filter((_, j) => j !== i); }}><Icon name="x" size={12} /></button>
                  </div>
                {/each}
                <button class="btn ghost sm addp" onclick={() => { const p = { name: "", value: "", kept: false }; if (k === "env") e.env = [...e.env, p]; else e.headers = [...e.headers, p]; }}><Icon name="plus" size={12} /> {k === "env" ? t("Variable") : t("Header")}</button>
              {/if}
            {/each}
            <div class="lbl">{t("Agents")}</div>
            <div class="frow wrap">
              {#each app.localAgents() as a (a.address)}
                {@const name = a.address.replace(/^agent:/, "")}
                <button class="chip pickc" class:on={e.agents.includes(name)} onclick={() => (e.agents = e.agents.includes(name) ? e.agents.filter(x => x !== name) : [...e.agents, name])}>{agentName(a.address)}</button>
              {/each}
              <span class="muted small">{e.agents.length ? "" : t("none chosen: all agents")}</span>
            </div>
            <div class="acts">
              {#if !e.isNew}<button class="btn ghost danger" onclick={() => remove(e.name)}>{t("Delete")}</button>{/if}
              <span class="sp"></span>
              <button class="btn" onclick={() => (edit = null)}>{t("Cancel")}</button>
              <button class="btn primary" disabled={busy || !e.name.trim()} onclick={save}>{t("Save")}</button>
            </div>
          </div>
        {/if}

      {:else if section === "playbooks"}
        <h2>{t("Playbooks")}</h2>
        <p class="muted">{t("Prompts for tasks you give again. Type / and the name in a new task.")}</p>
        {#if !pb}
          <div class="card panel"><div class="inner">
            {#each app.playbooks as p (p.name)}
              <div class="row">
                <Icon name="book" size={14} />
                <div class="t"><b class="mono">/{p.name}</b><span class="muted small one">{p.body.split("\n")[0]}</span></div>
                <button class="btn ghost sm" onclick={() => (pb = { isNew: false, name: p.name, body: p.body })}>{t("Edit")}</button>
              </div>
            {:else}<div class="row muted">{t("No playbooks yet")}</div>{/each}
          </div></div>
          <div class="acts left"><button class="btn primary" onclick={() => (pb = { isNew: true, name: "", body: "" })}><Icon name="plus" size={13} /> {t("New playbook")}</button></div>
        {:else}
          <div class="form card">
            {#if pb.isNew}<input class="input mono" placeholder={t("Name: review, release-notes")} bind:value={pb.name} />{:else}<div class="fh mono">/{pb.name}</div>{/if}
            <textarea class="input" rows="12" placeholder={t("What the agent should do, step by step")} bind:value={pb.body}></textarea>
            <div class="acts">
              {#if !pb.isNew}<button class="btn ghost danger" onclick={() => deletePlaybook(pb!.name)}>{t("Delete")}</button>{/if}
              <span class="sp"></span>
              <button class="btn" onclick={() => (pb = null)}>{t("Cancel")}</button>
              <button class="btn primary" disabled={busy || !pb.name.trim() || !pb.body.trim()} onclick={savePlaybook}>{t("Save")}</button>
            </div>
          </div>
        {/if}

      {:else if section === "knowledge"}
        <h2>{t("Knowledge")}</h2>
        <p class="muted">{t("Notes an agent gets at the start of every new session: everywhere, or only in a directory.")}</p>
        {#if !note}
          <div class="card panel"><div class="inner">
            {#each notes ?? [] as k (k.name)}
              <div class="row">
                <Icon name="brain" size={14} />
                <div class="t"><b>{k.name}</b><span class="muted small one">{k.scope ? tilde(k.scope) : t("Everywhere")} · {k.body.split("\n")[0]}</span></div>
                <button class="btn ghost sm" onclick={() => (note = { ...k })}>{t("Edit")}</button>
                <button class="sw" class:on={k.enabled} onclick={() => saveNote({ ...k, enabled: !k.enabled })} aria-label={t("Enabled")}><span></span></button>
              </div>
            {:else}<div class="row muted">{notes === null ? t("Loading…") : t("No notes yet")}</div>{/each}
          </div></div>
          <div class="acts left"><button class="btn primary" onclick={() => (note = { isNew: true, name: "", body: "", scope: "", enabled: true })}><Icon name="plus" size={13} /> {t("New note")}</button></div>
        {:else}
          <div class="form card">
            {#if note.isNew}<input class="input" placeholder={t("Name: how argus builds")} bind:value={note.name} />{:else}<div class="fh">{note.name}</div>{/if}
            <div class="frow">
              <button class="chip pickc" class:on={!note.scope} onclick={() => (note!.scope = "")}>{t("Everywhere")}</button>
              <button class="chip pickc" class:on={!!note.scope} onclick={pickNoteDir} title={note.scope}><Icon name="file" size={12} /> {note.scope ? tilde(note.scope) : t("In a directory…")}</button>
            </div>
            <textarea class="input" rows="12" placeholder={t("What the agent should know: how to build and test, conventions, what not to touch")} bind:value={note.body}></textarea>
            <div class="acts">
              {#if !note.isNew}<button class="btn ghost danger" onclick={() => deleteNote(note!.name)}>{t("Delete")}</button>{/if}
              <span class="sp"></span>
              <button class="btn" onclick={() => (note = null)}>{t("Cancel")}</button>
              <button class="btn primary" disabled={busy || !note.name.trim() || !note.body.trim()} onclick={() => saveNote(note!)}>{t("Save")}</button>
            </div>
          </div>
        {/if}

      {:else if section === "schedules"}
        <h2>{t("Schedules")}</h2>
        <p class="muted">{t("Tasks the node sends on a schedule. Each run is a new chat.")}</p>
        {#if !sc}
          <div class="card panel"><div class="inner">
            {#each scheds ?? [] as s (s.id)}
              <div class="row">
                <Icon name="clock" size={14} />
                <div class="t">
                  <b>{s.name}</b>
                  <span class="muted small one">{agentName(s.agent)} · <span class="mono">{s.cron}</span>{#if s.enabled && s.next_run} · {t("next {when}", { when: when(s.next_run) })}{/if}{#if s.last_run} · {t("last {when}", { when: when(s.last_run) })}{/if}</span>
                  {#if s.last_err}<span class="small bad one">{s.last_err}</span>{/if}
                </div>
                <button class="btn ghost sm" onclick={() => runSched(s.id!)}>{t("Run now")}</button>
                <button class="btn ghost sm" onclick={() => (sc = { ...s })}>{t("Edit")}</button>
                <button class="sw" class:on={s.enabled} onclick={() => saveSched({ ...s, enabled: !s.enabled })} aria-label={t("Enabled")}><span></span></button>
              </div>
            {:else}<div class="row muted">{scheds === null ? t("Loading…") : t("No schedules yet")}</div>{/each}
          </div></div>
          <div class="acts left"><button class="btn primary" onclick={() => (sc = newScheduleDraft(app.localAgents()[0]?.address ?? "", app.home))}><Icon name="plus" size={13} /> {t("New schedule")}</button></div>
        {:else}
          <div class="form card">
            <input class="input" placeholder={t("Name: morning review")} bind:value={sc.name} />
            <div class="lbl">{t("When")}</div>
            <div class="frow wrap">
              {#each presets as [l, c]}<button class="chip pickc" class:on={sc.cron === c} onclick={() => (sc!.cron = c)}>{t(l)}</button>{/each}
            </div>
            <input class="input mono" placeholder={t("cron: min hour day month weekday")} bind:value={sc.cron} />
            <div class="lbl">{t("Agent and directory")}</div>
            <div class="frow">
              <Dropdown bind:value={sc.agent} icon="bot" items={app.localAgents().map(a => ({ value: a.address, label: agentName(a.address) }))} />
              <button class="chip pickc" onclick={pickSchedDir} title={sc.cwd}><Icon name="file" size={12} /> {tilde(sc.cwd) || "~"}</button>
            </div>
            <div class="lbl">{t("Task")}</div>
            {#if app.playbooks.length}
              <div class="frow wrap">
                {#each app.playbooks as p (p.name)}<button class="chip pickc" class:on={sc.body === p.body} onclick={() => (sc!.body = p.body)}>/{p.name}</button>{/each}
              </div>
            {/if}
            <textarea class="input" rows="8" placeholder={t("What the agent should do")} bind:value={sc.body}></textarea>
            <div class="acts">
              {#if sc.id && !sc.isNew}<button class="btn ghost danger" onclick={() => deleteSched(sc!.id!)}>{t("Delete")}</button>{/if}
              <span class="sp"></span>
              <button class="btn" onclick={() => (sc = null)}>{t("Cancel")}</button>
              <button class="btn primary" disabled={busy || !sc.name.trim() || !sc.cron.trim() || !sc.agent || !sc.body.trim()} onclick={() => saveSched(sc!)}>{t("Save")}</button>
            </div>
          </div>
        {/if}

      {:else if section === "agents"}
        <h2>{t("Agents")}</h2>
        <p class="muted">{t("The agents of this Mac (in ~/.config/acpost/config.yaml), of other nodes and of the cloud.")}</p>
        <div class="card panel"><div class="inner">
          {#each app.agents as a (a.address)}
            <div class="row">
              <Icon name={a.driver === "sandbox" ? "cloud" : "bot"} size={14} />
              <div class="t"><b>{agentName(a.address)}</b><span class="muted small">{a.driver}{a.node ? " · " + a.node : ""}</span></div>
              {#if a.running}<span class="chip">{t("running")} {a.running}</span>{/if}
              {#if a.queued}<span class="chip warn">{a.queued} {t("queued")}</span>{/if}
            </div>
          {/each}
          <div class="row">
            <Icon name="cloud" size={14} />
            <div class="t"><b>{t("Cloud")}</b><span class="muted small">{app.cloudState === "down" ? t("The cloud is not answering") + " — " + app.cloudError : app.cloudState === "ok" ? t("{n} agents in the catalogue", { n: app.cloudAgents.length }) : t("optional: agents that run in sandboxes of an agent-sandbox cloud")}</span></div>
            <button class="btn ghost sm" onclick={() => { section = "cloud"; err = ""; }}>{app.cloudState ? t("Build agents") : t("Connect")}</button>
          </div>
        </div></div>

      {:else if section === "cloud"}
        <Cloud />

      {:else}
        <h2>{t("General")}</h2>
        <div class="card panel"><div class="inner">
          <div class="row">
            <div class="t"><b>{t("Notifications")}</b><span class="muted small">{t("When a turn ends or an agent waits for you, and the app is in the background")}</span></div>
            <button class="sw" class:on={notify} onclick={() => setNotify(!notify)} aria-label={t("Notifications")}><span></span></button>
          </div>
          <div class="row">
            <div class="t"><b>{t("Appearance")}</b></div>
            <div class="seg">
              <button class:on={app.theme === "light"} onclick={() => app.setTheme("light")}>{t("Light")}</button>
              <button class:on={app.theme === "dark"} onclick={() => app.setTheme("dark")}>{t("Dark")}</button>
              <button class:on={app.theme === "system"} onclick={() => app.setTheme("system")}>{t("Auto")}</button>
            </div>
          </div>
          <div class="row">
            <div class="t"><b>{t("Language")}</b></div>
            <div class="seg">
              {#each [["auto", "Auto"], ["en", "English"], ["ru", "Русский"]] as [v, l]}
                <button class:on={i18n.choice === v} onclick={() => i18n.set(v as Lang)}>{v === "auto" ? t(l) : l}</button>
              {/each}
            </div>
          </div>
          <div class="row">
            <div class="t"><b>{t("Text size")}</b><span class="muted small">{t("Also ⌘+ and ⌘−; ⌘0 for the usual size")}</span></div>
            <div class="size">
              <button class="btn ghost sm" onclick={() => app.stepTextSize(-1)} aria-label={t("Smaller")} disabled={app.textSize <= 0.85}><span class="a1">A</span></button>
              <button class="btn ghost sm pct num" onclick={() => app.setTextSize(1)} title={t("The usual size")}>{Math.round(app.textSize * 100)}%</button>
              <button class="btn ghost sm" onclick={() => app.stepTextSize(1)} aria-label={t("Larger")} disabled={app.textSize >= 1.5}><span class="a2">A</span></button>
            </div>
          </div>
          <div class="row">
            <div class="t"><b>{t("Node")}</b><span class="muted small mono">{app.conn?.url}</span></div>
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
  .size { display: flex; align-items: center; gap: 2px; }
  .size .a1 { font-size: 11px; font-weight: 600; }
  .size .a2 { font-size: 16px; font-weight: 600; }
  .size .pct { min-width: 52px; justify-content: center; font-size: 12px; }
  .err { white-space: pre-line; }
  .sw { width: 34px; height: 20px; border-radius: 10px; border: 0; background: var(--track); position: relative; flex: none; }
  .sw span { position: absolute; top: 2px; left: 2px; width: 16px; height: 16px; border-radius: 50%; background: var(--card); box-shadow: 0 1px 2px rgba(0,0,0,.2); transition: left .15s; }
  .sw.on { background: var(--good); }
  .sw.on span { left: 16px; }
</style>
