<script lang="ts">
  import AgentMark, { brandOf } from "./AgentMark.svelte";
  import type { Block } from "../lib/turn";
  import { app } from "../lib/state.svelte";

  // What a running agent is doing, in a word that changes, as Claude Code and Codex show it: the
  // tool it runs, else that it thinks — and, while nothing new comes, the words turn.
  let { agent, view, msg }: { agent: string; view?: { blocks: Block[]; tools: Map<string, any> }; msg: string } = $props();
  // Since the app first saw the turn running, kept across views.
  const since = $derived(app.seenRunning(msg));
  const brand = $derived(brandOf(agent));

  const words: Record<string, string[]> = {
    claude: ["Thinking", "Thinking some more", "Pondering", "Mulling it over", "Working it out", "Connecting the dots", "Weighing options", "Almost there"],
    codex: ["Working", "Thinking", "Exploring", "Reasoning", "Planning the change", "Checking the details"],
    "": ["Working", "Thinking", "Still on it"],
  };
  let tick = $state(0);
  let now = $state(Date.now());
  $effect(() => {
    const a = setInterval(() => tick++, 3200);
    const b = setInterval(() => (now = Date.now()), 1000);
    return () => { clearInterval(a); clearInterval(b); };
  });

  const doing = $derived.by(() => {
    const last = view?.blocks.at(-1);
    if (last?.t === "tool") {
      const t = view?.tools.get(last.id);
      if (t && t.status !== "completed" && t.status !== "failed" && t.title) return t.title;
    }
    const list = words[brand];
    if (last?.t === "thought") return list[tick % 2 === 0 ? 0 : 1];
    return list[tick % list.length];
  });
  const secs = $derived(Math.max(0, Math.round((now - since) / 1000)));
  const took = $derived(secs < 60 ? `${secs}s` : `${Math.floor(secs / 60)}m ${secs % 60}s`);
</script>

<span class="working">
  {#if brand}<AgentMark of={brand} size={13} busy />{:else}<span class="spin"></span>{/if}
  {#key doing}<span class="word" class:claude={brand === "claude"}>{doing}…</span>{/key}
  {#if secs}<span class="muted num">{took}</span>{/if}
</span>

<style>
  .working { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; min-width: 0; }
  .word {
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 360px; font-weight: 500;
    background: linear-gradient(90deg, var(--muted) 0%, var(--muted) 40%, var(--ink) 50%, var(--muted) 60%, var(--muted) 100%);
    background-size: 250% 100%; -webkit-background-clip: text; background-clip: text; color: transparent;
    animation: shimmer 2.2s linear infinite, enter .25s ease-out;
  }
  .word.claude { background-image: linear-gradient(90deg, #b8664c 0%, #b8664c 40%, #f0a789 50%, #b8664c 60%, #b8664c 100%); }
  @keyframes shimmer { from { background-position: 100% 0; } to { background-position: -150% 0; } }
  @keyframes enter { from { opacity: 0; transform: translateY(3px); } to { opacity: 1; transform: none; } }
  .num { font-size: 11px; }
</style>
