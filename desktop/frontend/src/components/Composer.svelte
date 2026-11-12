<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import Icon from "./Icon.svelte";
  import TaskSetup from "./TaskSetup.svelte";
  import { bytes } from "../lib/names";
  import { pasteFiles, pasteName, freeName } from "../lib/paste";
  import { who } from "../lib/format";
  import Avatar from "./Avatar.svelte";

  // The text and files are the chat's draft, kept by the app (they outlive this composer: a new
  // task becoming a chat, the node going down and coming back).
  const d = $derived(app.draftOf(app.draftKey()));
  let ta: HTMLTextAreaElement;
  let picker: HTMLInputElement;
  let pickerFor = $state<"" | "model" | "effort" | "mode" | "agent">("");
  let slashSel = $state(0);

  const isNew = $derived(!app.chat && !app.thread);
  // The agent the message goes to (for its options): a new task's choice, or the chat's.
  const agent = $derived(isNew ? (app.draft.where === "mac" ? app.draft.agent : "") : app.target);
  const opts = $derived(app.options[agent]);
  const byCat = (cat: string) => opts?.options.find(o => o.category === cat);
  const model = $derived(byCat("model"));
  const effort = $derived(byCat("thought_level"));
  const mode = $derived(byCat("mode"));
  const cloud = $derived(isNew ? app.draft.where === "cloud" : agent.startsWith("agent:sb_"));

  // A chat can be shared by several agents: who the next message goes to is chosen here (the chip,
  // or "@name" at the start of the message).
  const targets = $derived(isNew ? [] : (app.msgs.length, app.targets()));
  const at = $derived.by(() => {
    if (targets.length < 2) return [];
    const m = /^@(\S*)$/.exec(d.text);
    if (!m) return [];
    // The one named exactly first (Enter takes it): "@codex" is this node's, not codex@peer.
    const q = m[1].toLowerCase(), name = (a: string) => a.slice("agent:".length).toLowerCase();
    return targets.filter(a => name(a).startsWith(q)).sort((a, b) => Number(name(b) === q) - Number(name(a) === q));
  });
  function takeAgent(a: string | undefined, clear = false) {
    if (!a) return;
    app.setTarget(a);
    if (clear) d.text = "";
    pickerFor = "";
    ta?.focus();
  }

  $effect(() => { if (agent) app.loadOptions(agent); });
  // Files handed over by another part of the app (the preview's problems): attached here.
  $effect(() => {
    if (app.pendingFiles.length) {
      d.take(app.pendingFiles);
      app.pendingFiles = [];
      requestAnimationFrame(() => ta?.focus());
    }
  });
  // ⌘L and playbooks put text here.
  $effect(() => {
    if (app.quote) { d.text = (d.text ? d.text + "\n" : "") + app.quote; app.quote = ""; requestAnimationFrame(() => ta?.focus()); }
  });

  // The slash menu: the settings, then the agent's own commands.
  const slash = $derived.by(() => {
    if (!d.text.startsWith("/") || d.text.includes(" ") || d.text.includes("\n")) return [];
    const q = d.text.slice(1).toLowerCase();
    const items: { name: string; description: string; pick?: "model" | "effort" | "mode" }[] = [];
    if (model) items.push({ name: "model", description: t("Choose the model"), pick: "model" });
    if (effort) items.push({ name: "effort", description: t("Choose the reasoning effort"), pick: "effort" });
    if (mode) items.push({ name: "mode", description: t("Choose the mode"), pick: "mode" });
    // Playbooks: the prompts kept, then the agent's own commands (but for the settings above:
    // Claude Code has its /model too).
    const seen = new Set(items.map(i => i.name));
    for (const p of app.playbooks) {
      if (!seen.has(p.name)) { seen.add(p.name); items.push({ name: p.name, description: "", playbook: p.body } as any); }
    }
    for (const c of opts?.commands ?? []) {
      if (c.name && !seen.has(c.name)) { seen.add(c.name); items.push({ name: c.name, description: c.description ?? "" }); }
    }
    return items.filter(i => i.name.toLowerCase().startsWith(q));
  });

  // The settings are the agent's own: a Claude model is never sent to Codex.
  function current(o: typeof model, k: "model" | "effort" | "mode") {
    if (!o) return "";
    const v = app.setting(agent, k) || o.current;
    return o.values.find(x => x.value === v)?.name ?? v;
  }
  function setDraft(k: "model" | "effort" | "mode", v: string) {
    app.setSetting(agent, k, v);
    pickerFor = "";
    ta?.focus();
  }
  function takeSlash(i: number) {
    const it = slash[i];
    if (!it) return;
    if (it.pick) { d.text = ""; pickerFor = it.pick; return; }
    if ((it as any).playbook) { d.text = (it as any).playbook; ta?.focus(); return; }
    d.text = "/" + it.name + " ";
    ta?.focus();
  }

  // While a turn runs and nothing is typed, the send button stops it (and ⌃C does, anywhere).
  const running = $derived(!isNew && app.runningHere().length > 0);
  const stopMode = $derived(running && !d.hasContent);

  // The text and files leave the draft only once the node has them (app.submit).
  async function send() {
    // "@codex" and nothing else chooses who answers; with a message after it, the message goes to
    // that agent (app.submit).
    const named = isNew ? null : app.mention(d.text);
    if (named && !named.rest.trim() && !d.files.length && !d.reading.size) { takeAgent(named.to, true); return; }
    await app.submit(d);
    ta?.focus();
  }
  function key(e: KeyboardEvent) {
    if (at.length) {
      if (e.key === "ArrowDown") { e.preventDefault(); slashSel = (slashSel + 1) % at.length; return; }
      if (e.key === "ArrowUp") { e.preventDefault(); slashSel = (slashSel - 1 + at.length) % at.length; return; }
      if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) { e.preventDefault(); takeAgent(at[slashSel], true); return; }
    }
    if (slash.length) {
      if (e.key === "ArrowDown") { e.preventDefault(); slashSel = (slashSel + 1) % slash.length; return; }
      if (e.key === "ArrowUp") { e.preventDefault(); slashSel = (slashSel - 1 + slash.length) % slash.length; return; }
      if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) { e.preventDefault(); takeSlash(slashSel); return; }
    }
    if (e.key === "Escape") pickerFor = "";
    if (e.key === "Enter" && (e.metaKey || !e.shiftKey)) { e.preventDefault(); send(); }
  }
  // A file over the node's limits is said at once, not read (and not found out at sending).
  // Its name is chosen as it is attached (a name already attached gets " (2)"): the node refuses
  // two files of one name in a message.
  function add(list: FileList | File[] | null, name: (f: { name: string; type: string }, taken: string[]) => string = (f, taken) => freeName(f.name, taken)) {
    d.add(list ?? [], app.limits, (over, file) => {
      app.error = over === "read" ? t("Could not read {name}", { name: file })
        : over === "count" ? t("A message takes at most {n} files", { n: app.limits!.max_files })
        : t("The files are over the node's limit of {n} together", { n: bytes(app.limits!.max_files_size) });
    }, name);
  }
  // A paste with files (a screenshot, a copied image, files copied in Finder) attaches them as a
  // drop does; text pastes as usual (lib/paste.ts says which, for each mix). The textarea's paste
  // comes here too: it bubbles to the card. Names are chosen in the draft's queue as each file is
  // attached (lib/paste.ts pasteName), so pastes and drops still being read never share one.
  function paste(e: ClipboardEvent) {
    if (!e.clipboardData) return;
    const files = pasteFiles(e.clipboardData);
    if (!files.length) return;
    e.preventDefault();
    add(files, pasteName);
  }
  $effect(() => { if (ta) { ta.style.height = "auto"; ta.style.height = Math.min(ta.scrollHeight, 240) + "px"; } d.text; });
  $effect(() => { slash.length; at.length; slashSel = 0; });
</script>

<svelte:window
  onclick={e => { if (pickerFor && !(e.target as HTMLElement).closest(".menu, .opt")) pickerFor = ""; }}
  onkeydown={e => {
    if (e.key === "Escape") pickerFor = "";
    if (e.ctrlKey && !e.metaKey && e.key.toLowerCase() === "c" && running) { e.preventDefault(); app.stopHere(); }
  }} />

{#if isNew}<TaskSetup />{/if}

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="card comp" ondragover={e => e.preventDefault()} ondrop={e => { e.preventDefault(); add(e.dataTransfer?.files ?? null); }} onpaste={paste}>
  {#if slash.length}
    <div class="menu card">
      {#each slash as it, i (it.name)}
        <button class="mi" class:sel={i === slashSel} onmouseenter={() => (slashSel = i)} onclick={() => takeSlash(i)}>
          <span class="mono">/{it.name}</span>{#if (it as any).playbook}<span class="pb">{t("playbook")}</span>{/if}
        </button>
      {/each}
    </div>
  {/if}
  {#if at.length}
    <div class="menu card">
      {#each at as a, i (a)}
        <button class="mi who" class:sel={i === slashSel} onmouseenter={() => (slashSel = i)} onclick={() => takeAgent(a, true)}>
          <Avatar addr={a} size={16} /><span>{who(a).name}</span>{#if who(a).node}<span class="muted">@{who(a).node}</span>{/if}
        </button>
      {/each}
    </div>
  {/if}
  {#if pickerFor === "agent"}
    <div class="menu card pick">
      <div class="mh">{t("Who answers")}</div>
      {#each targets as a (a)}
        <button class="mi who" class:sel={a === agent} onclick={() => takeAgent(a)}>
          <Avatar addr={a} size={16} /><span>{who(a).name}</span>{#if who(a).node}<span class="muted">@{who(a).node}</span>{/if}
        </button>
      {/each}
    </div>
  {:else if pickerFor}
    {@const o = pickerFor === "model" ? model : pickerFor === "effort" ? effort : mode}
    {#if o}
      <div class="menu card pick">
        <div class="mh">{o.name}</div>
        {#each o.values as v (v.value)}
          <button class="mi" class:sel={(app.setting(agent, pickerFor) || o.current) === v.value} onclick={() => setDraft(pickerFor as any, v.value)}>
            <span>{v.name}</span>
          </button>
        {/each}
      </div>
    {/if}
  {/if}
  {#if d.files.length}
    <div class="files">
      {#each d.files as f, i}
        <span class="chip"><Icon name="file" size={12} /> {f.name} <span class="muted">{bytes(f.size)}</span>
          <button class="x" onclick={() => (d.files = d.files.filter((_, j) => j !== i))}><Icon name="x" size={10} /></button></span>
      {/each}
    </div>
  {/if}
  <textarea bind:this={ta} bind:value={d.text} onkeydown={key} rows="1"
    placeholder={isNew ? t("/ for the model, effort and commands") : targets.length > 1 ? t("Reply… (@ calls another agent)") : t("Reply…")}></textarea>
  <div class="bar">
    <button class="btn ghost sm" onclick={() => picker.click()} title={t("Attach files")}><Icon name="clip" size={14} /></button>
    <input bind:this={picker} type="file" multiple hidden onchange={e => add((e.target as HTMLInputElement).files)} />
    {#if targets.length > 1}<button class="chip opt to" onclick={() => (pickerFor = pickerFor === "agent" ? "" : "agent")} title={t("Who answers: another agent can be called into this chat")}><Avatar addr={agent} size={14} /> {who(agent).name}{#if who(agent).node}<span class="muted">@{who(agent).node}</span>{/if}</button>{/if}
    {#if model}<button class="chip opt" onclick={() => (pickerFor = pickerFor === "model" ? "" : "model")} title={t("Model")}>{current(model, "model")}</button>{/if}
    {#if effort}<button class="chip opt" onclick={() => (pickerFor = pickerFor === "effort" ? "" : "effort")} title={t("Reasoning effort")}><Icon name="brain" size={11} /> {current(effort, "effort")}</button>{/if}
    {#if mode}<button class="chip opt" onclick={() => (pickerFor = pickerFor === "mode" ? "" : "mode")} title={t("Mode")}>{current(mode, "mode")}</button>{/if}
    {#if cloud}<span class="chip muted" title={t("A cloud agent runs without asking; opening or merging a pull request waits for you")}>{t("runs without asking")}</span>{/if}
    <span class="sp"></span>
    <span class="hint muted">{stopMode ? "⌃C" : "⌘↵"}</span>
    {#if stopMode}
      <button class="btn primary send stop" title={t("Stop (⌃C)")} onclick={() => app.stopHere()}><span class="sq"></span></button>
    {:else}
      <button class="btn primary send" disabled={!d.hasContent || d.busy} onclick={send} title={running ? t("Queue after the running turn") : t("Send")}><Icon name="send" size={14} /></button>
    {/if}
  </div>
</div>

<style>
  .comp { position: relative; padding: 10px 10px 8px 14px; border-radius: var(--r-panel); }
  textarea { width: 100%; border: 0; outline: 0; resize: none; background: transparent; padding: 4px 2px 8px; font-size: 13.5px; line-height: 1.5; max-height: 240px; user-select: text; cursor: text; }
  .bar { display: flex; align-items: center; gap: 6px; }
  .sp { flex: 1; }
  .hint { font: 11px var(--mono); }
  .stop .sq { width: 10px; height: 10px; border-radius: 2.5px; background: currentColor; display: block; }
  .send { width: 30px; height: 30px; padding: 0; justify-content: center; border-radius: 10px; }
  .opt { height: 24px; cursor: default; }
  .opt:hover { background: var(--hover); }
  .files { display: flex; gap: 6px; flex-wrap: wrap; padding: 2px 0 6px; }
  .x { border: 0; background: transparent; color: var(--muted); padding: 0 0 0 2px; display: grid; }
  .menu { position: absolute; left: 10px; right: 10px; bottom: calc(100% + 6px); z-index: 30; padding: 6px; display: grid; max-height: 300px; overflow: auto; }
  .menu.pick { right: auto; min-width: 200px; max-width: 280px; }
  .pb { font-size: 10.5px; color: var(--accent); }
  .mh { font-size: 10.5px; font-weight: 600; color: var(--muted); text-transform: uppercase; padding: 4px 10px 6px; }
  .mi { display: flex; gap: 12px; align-items: baseline; border: 0; background: transparent; text-align: left; padding: 7px 10px; border-radius: 8px; font-size: 12.5px; }
  .mi.sel, .mi:hover { background: var(--hover); }
  .mi.who { align-items: center; gap: 8px; }
  .to { display: inline-flex; align-items: center; gap: 5px; }
</style>
