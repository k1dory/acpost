<script lang="ts">
  import claude from "../assets/claude.svg";
  import { brand as brandInfo } from "../lib/brand";
  // The mark of the coding agent behind an address or a name (see lib/brand): Claude's spark, the
  // drawn marks of Codex, Gemini, Copilot and Cursor, the others' initials on a tile in their hue;
  // animated while it works (busy). Nothing when no brand is known: the caller draws its own.
  let { of, size = 20, busy = false }: { of: string; size?: number; busy?: boolean } = $props();
  const b = $derived(brandInfo(markOf(of)));
</script>

<script module lang="ts">
  import { agentBrand } from "../lib/brand";
  import { app } from "../lib/state.svelte";
  /** The brand AgentMark draws for an address or a name: "" for none. */
  export function markOf(of: string | undefined) {
    return agentBrand(of, app.agents);
  }
</script>

{#if b}
  <span class="mark {b.id}" class:busy class:tile={!b.mark} style="width:{size}px;height:{size}px;--h:{b.hue};--s:{b.sat}%;font-size:{Math.max(6, Math.round(size * (b.tile.length > 1 ? 0.4 : 0.52)))}px" title={b.title}>
    {#if b.id === "claude"}
      <img src={claude} alt="" draggable="false" />
    {:else if b.id === "codex"}
      <!-- Six links turned about the centre: the OpenAI blossom, drawn in the text's colour. -->
      <svg viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round">
        {#each [0, 60, 120, 180, 240, 300] as r}<path d="M8.6 14V7.2a3.4 3.4 0 0 1 6.1-2.1" transform="rotate({r} 12 12)" />{/each}
      </svg>
    {:else if b.id === "gemini"}
      <!-- The four-pointed spark with curved sides. -->
      <svg viewBox="0 0 24 24" aria-hidden="true"><path fill="currentColor" d="M12 2c.6 5.3 4.7 9.4 10 10-5.3.6-9.4 4.7-10 10-.6-5.3-4.7-9.4-10-10 5.3-.6 9.4-4.7 10-10z" /></svg>
    {:else if b.id === "copilot"}
      <!-- The pilot's head: goggles over a round face with two eyes. -->
      <svg viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round">
        <rect x="3.6" y="3.8" width="7.4" height="6.6" rx="3" /><rect x="13" y="3.8" width="7.4" height="6.6" rx="3" />
        <path d="M3 11.5v3.2c0 2.8 4.2 5.5 9 5.5s9-2.7 9-5.5v-3.2M9.6 13.8v2M14.4 13.8v2" />
      </svg>
    {:else if b.id === "cursor"}
      <!-- The cube seen from a corner, its top face shaded. -->
      <svg viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round">
        <path d="M12 12l8.5-4.75L12 2.5 3.5 7.25z" fill="currentColor" fill-opacity=".35" stroke="none" />
        <path d="M12 2.5l8.5 4.75v9.5L12 21.5l-8.5-4.75v-9.5zM3.5 7.25L12 12l8.5-4.75M12 12v9.5" />
      </svg>
    {:else}
      {b.tile}
    {/if}
  </span>
{/if}

<style>
  .mark { display: inline-grid; place-items: center; flex: none; color: var(--ink); }
  .mark img, .mark svg { width: 100%; height: 100%; display: block; }
  /* A brand without a drawn mark: its initials on a tile in its hue, as an agent's badge is. */
  .tile {
    border-radius: 24%; font-weight: 650; letter-spacing: -.02em; line-height: 1;
    color: hsl(var(--h) var(--s) 30%); background: hsl(var(--h) var(--s) 90%);
    box-shadow: inset 0 0 0 1px hsl(var(--h) var(--s) 80%);
  }
  :global([data-theme="dark"]) .tile { color: hsl(var(--h) var(--s) 82%); background: hsl(var(--h) var(--s) 22%); box-shadow: inset 0 0 0 1px hsl(var(--h) var(--s) 32%); }
  /* Claude: the spark turns and breathes, as in Claude Code while it thinks. */
  .claude.busy img { animation: spark 2.4s cubic-bezier(.45, 0, .25, 1) infinite; }
  @keyframes spark {
    0% { transform: rotate(0) scale(1); }
    35% { transform: rotate(120deg) scale(.82); }
    60% { transform: rotate(210deg) scale(1.06); }
    100% { transform: rotate(360deg) scale(1); }
  }
  /* Codex, and every other mark: it breathes and its glow runs. */
  .busy:not(.claude) { animation: breathe 1.6s ease-in-out infinite; }
  @keyframes breathe {
    0%, 100% { transform: scale(1); filter: drop-shadow(0 0 0 rgba(122, 157, 255, 0)); }
    50% { transform: scale(.9); filter: drop-shadow(0 0 4px rgba(122, 157, 255, .75)); }
  }
  @media (prefers-reduced-motion: reduce) { .busy img, .busy:not(.claude) { animation-duration: 6s; } }
</style>
