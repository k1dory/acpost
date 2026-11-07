<script lang="ts">
  import { app } from "../lib/state.svelte";
  import { who } from "../lib/format";
  import Icon from "./Icon.svelte";

  let text = $state("");
  let files = $state<{ name: string; type: string; size: number; data: string }[]>([]);
  let busy = $state(false);
  let ta: HTMLTextAreaElement;
  let picker: HTMLInputElement;
  const cloud = $derived(app.agents.find(a => a.address === app.target)?.driver === "sandbox");

  async function send() {
    const body = text.trim();
    if (!body || busy) return;
    busy = true;
    await app.send(body, files.map(({ name, type, data }) => ({ name, type, data })));
    busy = false;
    text = "";
    files = [];
    ta?.focus();
  }
  function key(e: KeyboardEvent) {
    if (e.key === "Enter" && (e.metaKey || !e.shiftKey)) { e.preventDefault(); send(); }
  }
  async function add(list: FileList | null) {
    for (const f of list ?? []) {
      const buf = new Uint8Array(await f.arrayBuffer());
      let bin = "";
      for (let i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode(...buf.subarray(i, i + 0x8000));
      files = [...files, { name: f.name, type: f.type || "application/octet-stream", size: f.size, data: btoa(bin) }];
    }
  }
  function kb(n: number) { return n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(0)} KB` : `${(n / (1 << 20)).toFixed(1)} MB`; }
  $effect(() => { if (ta) { ta.style.height = "auto"; ta.style.height = Math.min(ta.scrollHeight, 240) + "px"; } text; });
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="card comp" ondragover={e => e.preventDefault()} ondrop={e => { e.preventDefault(); add(e.dataTransfer?.files ?? null); }}>
  {#if files.length}
    <div class="files">
      {#each files as f, i}
        <span class="chip"><Icon name="file" size={12} /> {f.name} <span class="muted">{kb(f.size)}</span>
          <button class="x" onclick={() => (files = files.filter((_, j) => j !== i))}><Icon name="x" size={10} /></button></span>
      {/each}
    </div>
  {/if}
  <textarea bind:this={ta} bind:value={text} onkeydown={key} rows="1" placeholder={app.thread ? "Reply…" : "What should the agent do?"}></textarea>
  <div class="bar">
    <button class="btn ghost sm" onclick={() => picker.click()} title="Attach files"><Icon name="clip" size={14} /></button>
    <input bind:this={picker} type="file" multiple hidden onchange={e => add((e.target as HTMLInputElement).files)} />
    <label class="agent">
      <Icon name={cloud ? "cloud" : "bot"} size={14} />
      <select bind:value={app.target}>
        {#each app.agents as a (a.address)}<option value={a.address}>{who(a.address).name}{a.node ? ` @${a.node}` : ""}{a.driver === "sandbox" ? " · cloud" : ""}</option>{/each}
      </select>
    </label>
    {#if cloud}<span class="chip muted" title="A cloud agent runs without asking; opening or merging a pull request waits for you">runs without asking</span>{/if}
    <span class="sp"></span>
    <span class="hint muted">⌘↵</span>
    <button class="btn primary send" disabled={!text.trim() || busy} onclick={send}><Icon name="send" size={14} /></button>
  </div>
</div>

<style>
  .comp { padding: 10px 10px 8px 14px; border-radius: var(--r-panel); }
  textarea { width: 100%; border: 0; outline: 0; resize: none; background: transparent; padding: 4px 2px 8px; font-size: 13.5px; line-height: 1.5; max-height: 240px; user-select: text; cursor: text; }
  .bar { display: flex; align-items: center; gap: 8px; }
  .sp { flex: 1; }
  .hint { font: 11px var(--mono); }
  .send { width: 30px; height: 30px; padding: 0; justify-content: center; border-radius: 10px; }
  .agent { display: inline-flex; align-items: center; gap: 6px; height: 26px; padding: 0 4px 0 8px; border-radius: 9px; border: 1px solid var(--line); color: var(--ink-2); }
  .agent select { border: 0; background: transparent; outline: 0; font-size: 12px; font-weight: 500; max-width: 200px; }
  .files { display: flex; gap: 6px; flex-wrap: wrap; padding: 2px 0 6px; }
  .x { border: 0; background: transparent; color: var(--muted); padding: 0 0 0 2px; display: grid; }
</style>
