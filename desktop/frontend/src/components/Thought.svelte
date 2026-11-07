<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { dur } from "../lib/format";
  import Icon from "./Icon.svelte";
  import AgentMark, { brandOf } from "./AgentMark.svelte";
  let { text, ms, live, agent = "" }: { text: string; ms: number; live: boolean; agent?: string } = $props();
  let open = $state(false);
</script>

<div class="th">
  <button class="head" onclick={() => (open = !open)}>
    {#if brandOf(agent)}<AgentMark of={agent} size={14} busy={live} />{:else}<Icon name="brain" size={14} />{/if}
    <span class:live>{live ? t("Thinking…") : ms >= 1000 ? t("Thought for {d}", { d: dur(ms) }) : t("Thought briefly")}</span>
    <span class="chev" class:rot={open}><Icon name="chev" size={12} /></span>
  </button>
  {#if open}<div class="body selectable">{text}</div>{/if}
</div>

<style>
  .th { margin: -2px 0; }
  .head { display: inline-flex; align-items: center; gap: 7px; border: 0; background: transparent; color: var(--muted); padding: 3px 6px; border-radius: 8px; font-size: 12px; }
  .live {
    background: linear-gradient(90deg, var(--muted) 0%, var(--muted) 40%, var(--ink) 50%, var(--muted) 60%, var(--muted) 100%);
    background-size: 250% 100%; -webkit-background-clip: text; background-clip: text; color: transparent; animation: shimmer 2.2s linear infinite;
  }
  @keyframes shimmer { from { background-position: 100% 0; } to { background-position: -150% 0; } }
  .head:hover { background: var(--hover); color: var(--ink-2); }
  .chev { display: grid; transition: transform .15s; } .chev.rot { transform: rotate(90deg); }
  .body { margin: 4px 0 6px 6px; padding-left: 12px; border-left: 2px solid var(--line); color: var(--ink-2); font-size: 13px; white-space: pre-wrap; max-height: 300px; overflow: auto; }
</style>
