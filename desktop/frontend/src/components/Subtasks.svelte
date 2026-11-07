<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { Node } from "../lib/api";
  import { who, ago } from "../lib/format";
  import AgentMark from "./AgentMark.svelte";

  // What the agent asked other agents while on this message (and what they asked in turn): a
  // tree, each to open in its own chat.
  let { msg, running = false }: { msg: string; running?: boolean } = $props();
  type Node_ = { id: string; parent: string; depth: number; from: string; to: string; thread: string; state: string; body: string; reply: string; at: string };
  let items = $state<Node_[]>([]);
  let open = $state(true);

  async function load() {
    try { items = ((await Node.Tree(msg)) ?? []) as any; } catch { items = []; }
  }
  // Depth first, each under its own parent (the node lists them level by level).
  const ordered = $derived.by(() => {
    const kids = new Map<string, Node_[]>();
    for (const n of items) kids.set(n.parent, [...(kids.get(n.parent) ?? []), n]);
    const out: Node_[] = [];
    const walk = (id: string, seen: Set<string>) => {
      for (const n of kids.get(id) ?? []) if (!seen.has(n.id)) { seen.add(n.id); out.push(n); walk(n.id, seen); }
    };
    walk(msg, new Set());
    return out;
  });
  // Refreshed while the message or any of what it asked still runs or waits.
  const live = $derived(running || items.some(n => n.state === "running" || n.state === "queued"));
  $effect(() => { msg; load(); });
  $effect(() => {
    if (!live) return;
    const id = setInterval(load, 4000);
    return () => clearInterval(id);
  });
  async function go(n: Node_) {
    await app.loadChats();
    app.openKey("thread:" + n.thread);
  }
</script>

{#if ordered.length}
  <div class="st">
    <button class="sh" onclick={() => (open = !open)}>
      <span class="chev" class:rot={open}>›</span> {t("Subtasks")} <span class="muted">{ordered.length}</span>
    </button>
    {#if open}
      {#each ordered as n (n.id)}
        <button class="it" style="padding-left:{8 + (n.depth - 1) * 16}px" onclick={() => go(n)} title={n.body}>
          <span class="dot {n.state === 'running' ? 'running' : n.state === 'queued' ? 'queued' : n.state === 'failed' || n.state === 'expired' ? 'failed' : ''}"></span>
          <AgentMark of={n.to} size={12} busy={n.state === "running"} />
          <span class="to">{who(n.to).name}</span>
          <span class="b">{n.body.split("\n")[0]}</span>
          <span class="muted num">{ago(n.at)}</span>
        </button>
      {/each}
    {/if}
  </div>
{/if}

<style>
  .st { margin-top: 8px; border: 1px solid var(--line-soft); border-radius: 12px; padding: 4px; display: grid; }
  .sh { border: 0; background: transparent; text-align: left; font-size: 12px; font-weight: 600; color: var(--ink-2); padding: 5px 8px; display: flex; gap: 6px; align-items: center; }
  .chev { display: inline-block; transition: transform .15s; }
  .chev.rot { transform: rotate(90deg); }
  .it { display: flex; align-items: center; gap: 7px; border: 0; background: transparent; text-align: left; padding: 5px 8px; border-radius: 8px; font-size: 12px; min-width: 0; }
  .it:hover { background: var(--hover); }
  .to { font-weight: 500; flex: none; }
  .b { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: var(--ink-2); }
  .num { font-size: 11px; flex: none; }
</style>
