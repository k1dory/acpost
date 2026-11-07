<script lang="ts">
  import claude from "../assets/claude.svg";
  import codex from "../assets/codex.svg";
  // The mark of the agent behind an address or a name: Claude's spark, Codex's; animated while it
  // works (busy). "" when it is neither: the caller draws its own.
  let { of, size = 20, busy = false }: { of: string; size?: number; busy?: boolean } = $props();
  const brand = $derived(brandOf(of));
</script>

<script module lang="ts">
  export function brandOf(s: string): "claude" | "codex" | "" {
    const v = (s ?? "").toLowerCase();
    return v.includes("claude") ? "claude" : v.includes("codex") ? "codex" : "";
  }
</script>

{#if brand}
  <span class="mark {brand}" class:busy style="width:{size}px;height:{size}px" title={brand === "claude" ? "Claude Code" : "Codex"}>
    <img src={brand === "claude" ? claude : codex} alt="" draggable="false" />
  </span>
{/if}

<style>
  .mark { display: inline-grid; place-items: center; flex: none; }
  .mark img { width: 100%; height: 100%; display: block; }
  .codex img { border-radius: 24%; }
  /* Claude: the spark turns and breathes, as in Claude Code while it thinks. */
  .claude.busy img { animation: spark 2.4s cubic-bezier(.45, 0, .25, 1) infinite; }
  @keyframes spark {
    0% { transform: rotate(0) scale(1); }
    35% { transform: rotate(120deg) scale(.82); }
    60% { transform: rotate(210deg) scale(1.06); }
    100% { transform: rotate(360deg) scale(1); }
  }
  /* Codex: the badge breathes and its glow runs. */
  .codex.busy img { animation: breathe 1.6s ease-in-out infinite; }
  @keyframes breathe {
    0%, 100% { transform: scale(1); filter: drop-shadow(0 0 0 rgba(122, 157, 255, 0)); }
    50% { transform: scale(.9); filter: drop-shadow(0 0 4px rgba(122, 157, 255, .75)); }
  }
  @media (prefers-reduced-motion: reduce) { .busy img { animation-duration: 6s; } }
</style>
