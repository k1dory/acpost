<script lang="ts">
  import { app } from "../lib/state.svelte";
  import { Node } from "../lib/api";
  import Icon from "./Icon.svelte";

  // Where a new task runs, which agent, in which directory (a repository in the cloud).
  const cloud = $derived(app.draft.where === "cloud");
  const local = $derived(app.localAgents());
  let dirsOpen = $state(false);
  let typed = $state("");
  function takeTyped(e: KeyboardEvent) {
    if (e.key === "Enter" && typed.trim()) {
      let d = typed.trim();
      if (d.startsWith("~/") && app.home) d = app.home + d.slice(1);
      app.draft.cwd = d;
      typed = "";
      dirsOpen = false;
    }
    if (e.key === "Escape") dirsOpen = false;
  }

  function label(addr: string) {
    const n = addr.replace(/^agent:/, "");
    return n === "claude" ? "Claude Code" : n === "codex" ? "Codex" : n;
  }
  function short(dir: string) {
    return app.home && dir.startsWith(app.home) ? "~" + dir.slice(app.home.length) : dir;
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
</script>

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
        <Icon name="file" size={12} /> {short(app.draft.cwd || app.home)} <Icon name="chev" size={10} />
      </button>
      {#if dirsOpen}
        <div class="menu card">
          <input class="input path" placeholder="A path: ~/src/project, then Enter" bind:value={typed} onkeydown={takeTyped} />
          {#each app.recentDirs() as d}
            <button class="mi" onclick={() => { app.draft.cwd = d; dirsOpen = false; }}>{short(d)}</button>
          {/each}
          <button class="mi strong" onclick={pick}>Choose a directory…</button>
        </div>
      {/if}
    </div>
  {:else}
    <select class="input sel" bind:value={app.draft.cloudAgent}>
      {#each app.cloudAgents as a (a.ref)}<option value={a.ref}>{a.name}{a.description ? " — " + a.description : ""}</option>{/each}
    </select>
    <input class="input repo" placeholder="Repository (optional)" bind:value={app.draft.repo} />
  {/if}
</div>

<style>
  .setup { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; margin-bottom: 10px; }
  .seg button { display: inline-flex; align-items: center; gap: 5px; }
  .seg button:disabled { opacity: .4; }
  .dir { position: relative; }
  .pick { height: 28px; padding: 0 10px; font-size: 12px; gap: 6px; cursor: default; }
  .menu { position: absolute; left: 0; bottom: 34px; z-index: 30; min-width: 280px; max-height: 320px; overflow: auto; padding: 6px; display: grid; }
  .path { height: 28px; font: 12px var(--mono); padding: 0 10px; border-radius: 8px; margin-bottom: 4px; }
  .mi { border: 0; background: transparent; text-align: left; padding: 7px 10px; border-radius: 8px; font: 12px var(--mono); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .mi:hover { background: var(--hover); }
  .mi.strong { font: 500 12.5px var(--sans); border-top: 1px solid var(--line-soft); margin-top: 4px; padding-top: 9px; border-radius: 0 0 8px 8px; }
  .sel { width: auto; height: 28px; padding: 0 8px; border-radius: 10px; font-size: 12px; max-width: 320px; }
  .repo { width: 260px; height: 28px; padding: 0 10px; border-radius: 10px; font-size: 12px; }
</style>
