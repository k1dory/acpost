<script lang="ts">
  import { app } from "../lib/state.svelte";
  import { Node } from "../lib/api";
  import Icon from "./Icon.svelte";
  import Dropdown from "./Dropdown.svelte";

  // Where a new task runs, which agent, in which directory (a repository in the cloud).
  const cloud = $derived(app.draft.where === "cloud");
  const local = $derived(app.localAgents());
  let dirsOpen = $state(false);
  let typed = $state("");
  let found = $state<string[]>([]);
  let sel = $state(0);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let seq = 0;

  function label(addr: string) {
    const n = addr.replace(/^agent:/, "");
    return n === "claude" ? "Claude Code" : n === "codex" ? "Codex" : n;
  }
  function short(dir: string) {
    return app.home && dir.startsWith(app.home) ? "~" + dir.slice(app.home.length) : dir;
  }
  // What the menu shows: the recent directories that match, then what the search found.
  const shown = $derived.by(() => {
    const q = typed.trim().toLowerCase();
    const recent = app.recentDirs().filter(d => !q || short(d).toLowerCase().includes(q) || d.toLowerCase().includes(q));
    const seen = new Set(recent);
    return [...recent, ...found.filter(d => !seen.has(d))].slice(0, 40);
  });
  $effect(() => {
    const q = typed;
    clearTimeout(timer);
    sel = 0;
    if (!q.trim()) { found = []; return; }
    const my = ++seq;
    timer = setTimeout(async () => {
      const r = ((await Node.SearchDirs(q).catch(() => [])) ?? []) as string[];
      if (my === seq) found = r;
    }, 120);
  });

  function take(d: string) {
    app.draft.cwd = d;
    typed = "";
    found = [];
    dirsOpen = false;
  }
  function key(e: KeyboardEvent) {
    if (e.key === "ArrowDown") { e.preventDefault(); sel = Math.min(sel + 1, shown.length - 1); }
    else if (e.key === "ArrowUp") { e.preventDefault(); sel = Math.max(sel - 1, 0); }
    else if (e.key === "Tab" && shown[sel]) { e.preventDefault(); typed = short(shown[sel]) + "/"; }
    else if (e.key === "Enter") {
      e.preventDefault();
      const t = typed.trim();
      if (shown[sel]) take(shown[sel]);
      else if (t.startsWith("/") || t.startsWith("~")) take(t.startsWith("~") && app.home ? app.home + t.slice(1) : t);
    } else if (e.key === "Escape") dirsOpen = false;
  }
  async function pick() {
    dirsOpen = false;
    const d = await Node.PickDirectory(app.draft.cwd || app.home).catch(() => "");
    if (d) app.draft.cwd = d;
  }
  function chooseAgent(a: string) {
    app.draft.agent = a;
    app.draft.model = app.draft.effort = app.draft.mode = "";
    app.loadOptions(a);
  }
  function focusOnMount(el: HTMLInputElement) { requestAnimationFrame(() => el.focus()); }
</script>

<svelte:window onclick={e => { if (dirsOpen && !(e.target as HTMLElement).closest(".dir")) dirsOpen = false; }} />

<div class="setup">
  <div class="seg">
    <button class:on={!cloud} onclick={() => (app.draft.where = "mac")}><Icon name="bot" size={13} /> This Mac</button>
    <button class:on={cloud} disabled={!app.cloudAgents.length} title={app.cloudAgents.length ? "" : "No cloud configured"} onclick={() => (app.draft.where = "cloud")}><Icon name="cloud" size={13} /> Cloud</button>
  </div>

  {#if !cloud}
    <div class="seg">
      {#each local as a (a.address)}
        <button class:on={app.draft.agent === a.address} onclick={() => chooseAgent(a.address)}>{label(a.address)}</button>
      {/each}
    </div>
    <div class="dir">
      <button class="chip pick" onclick={() => (dirsOpen = !dirsOpen)} title={app.draft.cwd}>
        <Icon name="file" size={12} /> <span class="cwd">{short(app.draft.cwd || app.home)}</span> <Icon name="chev" size={10} />
      </button>
      {#if dirsOpen}
        <div class="menu card">
          <div class="searchrow">
            <Icon name="search" size={13} />
            <input class="q" placeholder="Search a directory, or a path: ~/src/…" bind:value={typed} onkeydown={key} use:focusOnMount />
          </div>
          <div class="items">
            {#each shown as d, i (d)}
              <button class="mi" class:on={i === sel} onmouseenter={() => (sel = i)} onclick={() => take(d)}>
                <span class="nm">{d.split("/").filter(Boolean).pop() || "/"}</span><span class="pth">{short(d)}</span>
              </button>
            {:else}
              <div class="none muted">{typed.trim() ? "Nothing found" : "No recent directories"}</div>
            {/each}
          </div>
          <button class="mi strong" onclick={pick}>Choose in Finder…</button>
        </div>
      {/if}
    </div>
  {:else}
    <Dropdown bind:value={app.draft.cloudAgent} icon="cloud" items={app.cloudAgents.map(a => ({ value: a.ref, label: a.name }))} />
    <input class="input repo" placeholder="Repository (optional)" bind:value={app.draft.repo} />
  {/if}
</div>

<style>
  .setup { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; justify-content: center; margin-bottom: 12px; }
  .seg button { display: inline-flex; align-items: center; gap: 5px; }
  .seg button:disabled { opacity: .4; }
  .dir { position: relative; }
  .pick { height: 28px; padding: 0 10px; font-size: 12px; gap: 6px; cursor: default; max-width: 340px; }
  .cwd { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; direction: rtl; text-align: left; }
  .menu { position: absolute; left: 50%; transform: translateX(-50%); top: 34px; z-index: 30; width: 440px; padding: 6px; display: grid; }
  .searchrow { display: flex; align-items: center; gap: 8px; padding: 4px 8px 8px; color: var(--muted); border-bottom: 1px solid var(--line-soft); margin-bottom: 4px; }
  .q { flex: 1; border: 0; outline: 0; background: transparent; font-size: 13px; color: var(--ink); }
  .items { max-height: 300px; overflow: auto; display: grid; }
  .mi { display: flex; align-items: baseline; gap: 10px; border: 0; background: transparent; text-align: left; padding: 6px 10px; border-radius: 8px; min-width: 0; }
  .mi.on { background: var(--hover); }
  .nm { font-weight: 500; font-size: 12.5px; flex: none; }
  .pth { font: 11px var(--mono); color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .mi.strong { font: 500 12.5px var(--sans); border-top: 1px solid var(--line-soft); margin-top: 4px; padding-top: 9px; border-radius: 0 0 8px 8px; }
  .none { font-size: 12px; padding: 8px 10px; }
  .repo { width: 260px; height: 28px; padding: 0 10px; border-radius: 10px; font-size: 12px; }
</style>
