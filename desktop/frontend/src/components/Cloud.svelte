<script lang="ts">
  import { t, tr, i18n } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { Node } from "../lib/api";
  import { agentLabel, tilde } from "../lib/names";
  import { withCurrent } from "../lib/cloud";
  import { CloudBuilder } from "../lib/cloudbuild.svelte";
  import Icon from "./Icon.svelte";
  import Dropdown from "./Dropdown.svelte";

  // The cloud: connecting one (a tenant's key), and building its agents (lib/cloudbuild).
  const cb = new CloudBuilder();
  cb.load();
  const info = $derived(cb.info), cat = $derived(cb.cat), ed = $derived(cb.ed), busy = $derived(cb.busy);
  const err = $derived(cb.err), note = $derived(cb.note), checked = $derived(cb.checked), missing = $derived(cb.missing);
  const drafting = $derived(cb.drafting), draft = $derived(cb.draft), draftNote = $derived(cb.draftNote), sure = $derived(cb.sure);

  // Connecting: the address and the key.
  let form = $state(false);
  let url = $state("");
  let key = $state("");
  let ca = $state("");
  const turns = $derived(app.agents.reduce((n, a: any) => n + (a.running ?? 0), 0));
  async function connect() {
    if (await cb.connect(url, key, ca)) { key = ""; form = false; }
  }
  function when(at?: string) {
    return at ? new Date(at).toLocaleDateString(i18n.lang === "ru" ? "ru-RU" : undefined, { day: "numeric", month: "short", year: "numeric" }) : "";
  }
  const soon = $derived(!!info?.expires && new Date(info.expires).getTime() - Date.now() < 7 * 86400e3);

  const locals = $derived(app.agents.filter((a: any) => a.driver === "acp" && !a.node));
  $effect(() => { if (!locals.some((a: any) => a.address === cb.draftAgent)) cb.draftAgent = (locals[0] as any)?.address ?? ""; });
  async function pickDir() {
    const d = await Node.PickDirectory(cb.draftDir || app.home).catch(() => "");
    if (d) cb.draftDir = d;
  }
</script>

<h2>{t("Cloud")}</h2>
<p class="muted">{t("Agents that run on their own in sandboxes of an agent-sandbox cloud. Optional: everything else works on this Mac alone.")}</p>
{#if err}<div class="err selectable">{err}</div>{/if}
{#if note}<div class="ok selectable">{note}</div>{/if}

<div class="card panel"><div class="inner">
  <div class="row">
    <Icon name="cloud" size={14} />
    {#if info === null}
      <span class="sk" style="width:40%;height:12px"></span>
    {:else if !info.configured}
      <div class="t"><b>{t("No cloud connected")}</b><span class="muted small">{t("Connect one with its address and a tenant's key")}</span></div>
    {:else}
      <div class="t">
        <b class="mono one">{info.url}</b>
        {#if info.error}<span class="bad small">{info.error_code === "refused" ? t("The cloud did not accept the key") : t("The cloud is not answering")} — {tr(info.error)}</span>
        {:else}<span class="muted small">{info.admin ? t("the platform's admin: the shared catalogue") : t("tenant {name}", { name: info.tenant ?? "" })}{info.subject ? " · " + info.subject : ""}{info.expires ? " · " + t("the key ends {at}", { at: when(info.expires) }) : ""}</span>{/if}
      </div>
      {#if info.error}<span class="chip crit">{t("unavailable")}</span>{:else if soon}<span class="chip warn">{t("the key ends soon")}</span>{:else}<span class="chip good">{t("connected")}</span>{/if}
    {/if}
    {#if info && !form}<button class="btn ghost sm" onclick={() => { form = true; url = info?.url ?? ""; }}>{info.configured ? t("Change the key") : t("Connect")}</button>{/if}
  </div>
</div></div>

{#if form}
  <div class="form card">
    <div class="fh">{t("Connect a cloud")}</div>
    <input class="input" placeholder={t("Address: https://sandbox.example.com")} bind:value={url} autocomplete="off" spellcheck="false" />
    <input class="input" type="password" placeholder={t("The tenant's key: sbk_…")} bind:value={key} autocomplete="off" />
    <input class="input" placeholder={t("CA certificate file (only for a private CA)")} bind:value={ca} autocomplete="off" spellcheck="false" />
    <div class="help muted small selectable">
      <p>{t("Where the key comes from: the cloud's admin issues a tenant's first key, and it is shown once:")}</p>
      <code>{"sandboxctl keys create -tenant NAME -name acpost"}</code>
      <p>{t("With a key, the tenant issues and revokes its own:")} <code>{"sandboxctl keys create -name acpost -ttl 720h"}</code></p>
      <p>{t("The app asks the cloud whose key it is, then keeps it in ~/.config/acpost/cloud.env, readable by you alone, and starts its node again.")}</p>
    </div>
    {#if turns}<div class="warn small">{t("Turns are running now: starting the node again interrupts them.")}</div>{/if}
    <div class="acts">
      <button class="btn" onclick={() => { form = false; key = ""; }}>{t("Cancel")}</button>
      <button class="btn primary" disabled={!!busy || !url.trim() || !key.trim()} onclick={connect}>{busy === "connect" ? t("Checking…") : t("Connect")}</button>
    </div>
  </div>
{/if}

{#if cat && !ed}
  <div class="lbl">{t("Agents in the catalogue")}</div>
  <div class="card panel"><div class="inner">
    {#each cat.agents as a (a.ref)}
      {@const mine = !a.shared || !!info?.admin}
      <div class="row">
        <Icon name="bot" size={14} />
        <div class="t"><b>{a.name}</b><span class="muted small one">{a.description || a.policy}{a.repo ? " · " + a.repo : ""}</span></div>
        {#if a.shared}<span class="chip">{t("shared")}</span>{/if}
        <span class="chip num">{t("revision {n}", { n: a.revision })}</span>
        {#if mine}<button class="btn ghost sm" disabled={!!busy} onclick={() => cb.editAgent(a)}>{t("Edit")}</button>
        {:else}<button class="btn ghost sm" onclick={() => cb.newAgent(a)} title={t("A shared agent is the admin's: yours starts as its copy")}>{t("Copy")}</button>{/if}
      </div>
    {:else}<div class="row muted">{t("No agents yet")}</div>{/each}
  </div></div>
  <div class="acts left"><button class="btn primary" onclick={() => cb.newAgent()}><Icon name="plus" size={13} /> {t("New agent")}</button></div>
{/if}

{#if cat && ed}
  {@const e = ed}
  <div class="form card">
    <div class="fh">{e.isNew ? t("New cloud agent") : e.spec.name}{#if !e.isNew}<span class="muted small"> · {t("revision {n}", { n: e.rev })}</span>{/if}</div>

    <div class="draft">
      <div class="lbl first">{t("Have an agent write it")}</div>
      <textarea class="input" rows="3" placeholder={t("What this agent is for: a backend developer for the shop, reviews pull requests, keeps the docs up to date…")} bind:value={cb.ask} disabled={drafting}></textarea>
      <div class="frow wrap">
        {#if locals.length}
          <Dropdown bind:value={cb.draftAgent} icon="bot" items={locals.map((a: any) => ({ value: a.address, label: agentLabel(a.address) }))} />
          <button class="chip pickc" onclick={pickDir} title={t("A repository the agent reads first, to write instructions that fit it")}><Icon name="file" size={12} /> {cb.draftDir ? tilde(cb.draftDir, app.home) : t("No repository")}</button>
          {#if cb.draftDir}<button class="btn ghost sm" onclick={() => (cb.draftDir = "")} aria-label={t("No repository")}><Icon name="x" size={11} /></button>{/if}
          <span class="sp"></span>
          {#if draft}<button class="btn ghost sm" onclick={() => cb.openDraft()}><Icon name="chat" size={12} /> {t("Open its chat")}</button>{/if}
          {#if drafting}<button class="btn sm" onclick={() => cb.stopDraft()}>{t("Stop")}</button>{/if}
          <button class="btn sm" disabled={drafting || !cb.ask.trim() || !cb.draftAgent} onclick={() => cb.writeDraft()}>{drafting ? t("Writing…") : t("Write a draft")}</button>
        {:else}
          <span class="muted small">{t("No agent of this Mac to write it: Claude Code or Codex over ACP")}</span>
        {/if}
      </div>
      {#if draftNote}<div class="muted small">{draftNote}</div>{/if}
      <div class="muted small">{t("The agent only reads and answers; nothing reaches the cloud until you deploy.")}</div>
    </div>

    <div class="frow">
      {#if e.isNew}<input class="input" placeholder={t("Name: backend, docs-writer")} bind:value={e.spec.name} spellcheck="false" />{/if}
      <input class="input" placeholder={t("What it is for, in a line")} bind:value={e.spec.description} />
    </div>
    <div class="frow wrap">
      <span class="lbl in">{t("Policy")}</span>
      <Dropdown bind:value={e.spec.policy} items={withCurrent(cat.policies.map(p => ({ value: p, label: p })), e.spec.policy)} placeholder={t("Choose")} />
      <span class="lbl in">{t("Environment")}</span>
      <Dropdown bind:value={e.spec.environment} items={withCurrent(cat.environments.map(v => ({ value: v.ref, label: v.ref })), e.spec.environment)} placeholder={t("Choose")} />
    </div>
    <div class="muted small">{t("The policy is what the agent may reach and with which credentials; the environment is its image, harness and model. Both are made in the cloud (sandboxctl apply).")}</div>
    {#if !cat.environments.length}<div class="warn small">{t("The cloud has no environments yet: an agent needs one.")}</div>{/if}
    <input class="input" placeholder={t("Repository the cloud clones: https://… (optional)")} bind:value={e.spec.repo} spellcheck="false" />
    {#if cat.connections.length || e.spec.connections.length}
      <div class="lbl">{t("Connections")}</div>
      <div class="frow wrap">
        {#each [...new Set([...cat.connections.map(c => c.ref), ...e.spec.connections])] as c (c)}
          <button class="chip pickc" class:on={e.spec.connections.includes(c)} onclick={() => cb.toggleConn(c)}>{c}</button>
        {/each}
      </div>
    {/if}
    <div class="lbl">{t("Instructions")}</div>
    <textarea class="input mono" rows="12" placeholder={t("What the agent is told before every task: the project, how to build and test it, the conventions, how to hand work in")} bind:value={e.spec.instructions}></textarea>

    {#if e.plan}
      <div class="plan" class:stale={!checked}>
        {#if !checked}<div class="muted small">{t("Changed since the check: check again")}</div>
        {:else if !e.plan.valid}
          <b class="bad">{t("The cloud refuses this definition")}</b>
          {#each e.plan.errors as x}<div class="bad small selectable">{tr(x)}</div>{/each}
        {:else if e.plan.action === "noop"}
          <b>{t("Nothing to deploy: the catalogue has exactly this")}</b>
        {:else}
          <b>{e.plan.action === "create" ? t("Deploy will add {name} as revision {n}", { name: e.spec.name.trim(), n: e.plan.next_revision }) : t("Deploy will write revision {n} (now {m})", { n: e.plan.next_revision, m: e.plan.revision })}</b>
          {#each e.plan.diff as d}
            <div class="dl mono small selectable"><span class="muted">{d.path}</span>{#if d.from}<span class="del">{d.from}</span>{/if}{#if d.to}<span class="add">{d.to}</span>{/if}</div>
          {/each}
        {/if}
      </div>
    {/if}
    <div class="muted small">{t("Check asks the cloud what would change; nothing is written. Deploy writes exactly that as a new revision. Nothing is built or restarted: a new cloud task starts from the latest revision, sandboxes made earlier keep theirs.")}</div>
    <div class="acts">
      {#if !e.isNew}<button class="btn ghost danger" disabled={!!busy} onclick={() => cb.remove()}>{sure === e.spec.name ? t("Delete it from the catalogue?") : t("Delete")}</button>{/if}
      <span class="sp"></span>
      {#if missing}<span class="muted small">{t(missing)}</span>{/if}
      <button class="btn" onclick={() => cb.close()}>{t("Cancel")}</button>
      <button class="btn" disabled={!!busy || !!missing} onclick={() => cb.check()}>{busy === "check" ? t("Checking…") : t("Check")}</button>
      <button class="btn primary" disabled={!!busy || !checked || !e.plan?.valid || e.plan?.action === "noop"} onclick={() => cb.deploy()}>{busy === "deploy" ? t("Deploying…") : t("Deploy")}</button>
    </div>
  </div>
{/if}

<style>
  h2 { font-size: 15px; margin: 0 0 4px; }
  .row { display: flex; align-items: center; gap: 12px; padding: 0 14px; min-height: 52px; border-bottom: 1px solid var(--line-soft); }
  .row:last-child { border-bottom: 0; }
  .t { flex: 1; min-width: 0; display: grid; }
  .small { font-size: 11.5px; }
  .one { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .bad { color: var(--crit); }
  .warn { color: var(--warn); }
  .form { padding: 16px; display: grid; gap: 10px; }
  .fh { font-weight: 600; }
  .frow { display: flex; gap: 8px; align-items: center; }
  .frow.wrap { flex-wrap: wrap; }
  .frow .input { flex: 1; }
  .input { height: 32px; font-size: 13px; }
  textarea.input { height: auto; resize: vertical; }
  textarea.mono { font-size: 12px; line-height: 1.5; }
  .lbl { font-size: 11px; font-weight: 600; color: var(--muted); text-transform: uppercase; margin-top: 4px; }
  .lbl.first, .lbl.in { margin-top: 0; }
  .acts { display: flex; gap: 8px; justify-content: flex-end; align-items: center; }
  .acts.left { justify-content: flex-start; }
  .sp { flex: 1; }
  .err, .ok { padding: 10px 12px; border-radius: 12px; font-size: 12.5px; white-space: pre-line; }
  .err { background: var(--crit-bg); color: var(--crit); }
  .ok { background: var(--good-bg); color: var(--good-text); }
  .pickc { cursor: default; height: 26px; }
  .pickc.on { background: var(--accent-bg); color: var(--accent); border-color: transparent; }
  .help { display: grid; gap: 6px; padding: 10px 12px; border-radius: 12px; background: var(--hover); }
  .help p { margin: 0; }
  code { font-family: var(--mono, ui-monospace, monospace); font-size: 11.5px; color: var(--ink); }
  .draft { display: grid; gap: 8px; padding: 12px; border-radius: 12px; border: 1px dashed var(--line); }
  .plan { display: grid; gap: 6px; padding: 10px 12px; border-radius: 12px; border: 1px solid var(--line-soft); background: var(--card); }
  .plan.stale { opacity: .6; }
  .dl { display: grid; gap: 2px; padding-top: 4px; border-top: 1px solid var(--line-soft); }
  .dl span { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 9em; overflow: auto; }
  .del { color: var(--crit); text-decoration: line-through; }
  .add { color: var(--good-text); }
</style>
