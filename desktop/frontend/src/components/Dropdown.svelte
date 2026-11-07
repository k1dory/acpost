<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import Icon from "./Icon.svelte";
  import { screenScale } from "../lib/format";

  // The app's own dropdown (never the system's): a chip, and a menu of names under it.
  let { value = $bindable(), items, placeholder = "Choose", icon = "", up = false }: {
    value: string; items: { value: string; label: string }[]; placeholder?: string; icon?: string; up?: boolean;
  } = $props();
  let open = $state(false);
  let sel = $state(0);
  let root: HTMLDivElement;
  let menu = $state<HTMLDivElement>();
  // The menu is placed on the screen (fixed), so no card around it cuts it; kept inside the window.
  let pos = $state("");
  $effect(() => {
    if (!open || !menu) return;
    // In the page's coordinates (the app's zoom taken out): what fixed positions are in.
    const k = screenScale();
    const r = root.getBoundingClientRect(), m = menu.getBoundingClientRect();
    const W = window.innerWidth / k, H = window.innerHeight / k;
    const rl = r.left / k, rt = r.top / k, rb = r.bottom / k, mw = m.width / k, mh = m.height / k;
    const left = Math.max(8, Math.min(rl, W - mw - 8));
    const below = rb + 4 + mh <= H - 8;
    const top = up || !below ? Math.max(8, rt - 4 - mh) : rb + 4;
    pos = `left:${left}px;top:${top}px`;
  });
  const current = $derived(items.find(i => i.value === value));

  function toggle() {
    if (!items.length) return; // nothing to choose: no empty menu
    open = !open;
    pos = "visibility:hidden";
    sel = Math.max(0, items.findIndex(i => i.value === value));
  }
  function take(v: string) { value = v; open = false; }
  function key(e: KeyboardEvent) {
    if (!open) { if (e.key === "ArrowDown" || e.key === "Enter" || e.key === " ") { e.preventDefault(); toggle(); } return; }
    if (e.key === "ArrowDown") { e.preventDefault(); sel = Math.min(sel + 1, items.length - 1); }
    else if (e.key === "ArrowUp") { e.preventDefault(); sel = Math.max(sel - 1, 0); }
    else if (e.key === "Enter") { e.preventDefault(); if (items[sel]) take(items[sel].value); }
    else if (e.key === "Escape") open = false;
  }
</script>

<!-- Fixed on the screen: scrolling under it or resizing the window closes it, as a system menu does. -->
<svelte:window onclick={e => { if (open && !root.contains(e.target as Node)) open = false; }}
  onresize={() => (open = false)}
  onscrollcapture={e => { if (open && !(menu && menu.contains(e.target as Node))) open = false; }} />

<div class="dd" bind:this={root}>
  <button class="chip trig" onclick={toggle} onkeydown={key}>
    {#if icon}<Icon name={icon} size={12} />{/if}
    <span class="lbl">{current?.label ?? t(placeholder)}</span>
    <span class="chev" class:rot={open}><Icon name="chev" size={10} /></span>
  </button>
  {#if open}
    <div class="menu card" bind:this={menu} style={pos}>
      {#each items as it, i (it.value)}
        <button class="mi" class:on={i === sel} onmouseenter={() => (sel = i)} onclick={() => take(it.value)}>
          <span class="ck">{#if it.value === value}<Icon name="check" size={12} />{/if}</span>{it.label}
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .dd { position: relative; display: inline-block; }
  .trig { height: 28px; padding: 0 9px; font-size: 12px; gap: 6px; cursor: default; max-width: 240px; }
  .trig:hover { background: var(--hover); }
  .lbl { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .chev { display: grid; transform: rotate(90deg); transition: transform .15s; color: var(--muted); }
  .chev.rot { transform: rotate(-90deg); }
  .menu { position: fixed; z-index: 60; min-width: 170px; max-width: 280px; max-height: 300px; overflow: auto; padding: 5px; display: grid; }
  .mi { display: flex; align-items: center; gap: 6px; border: 0; background: transparent; text-align: left; padding: 6px 9px 6px 6px; border-radius: 8px; font-size: 12.5px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .mi.on { background: var(--hover); }
  .ck { width: 14px; display: grid; place-items: center; color: var(--accent); flex: none; }
</style>
