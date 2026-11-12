<script lang="ts">
  import { focusOnMount } from "../lib/actions";
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import Icon from "./Icon.svelte";

  // ⌘K: chats, actions and playbooks, as you type.
  let q = $state("");
  let sel = $state(0);
  type Item = { kind: string; label: string; sub?: string; icon: string; run: () => void };
  const actions: Item[] = [
    { kind: "Action", label: "New task", icon: "plus", run: () => app.newTask() },
    { kind: "Action", label: "Tasks", icon: "activity", run: () => (app.view = "board") },
    { kind: "Action", label: "Wiki", icon: "book", run: () => (app.view = "wiki") },
    { kind: "Action", label: "IDE (VS Code)", icon: "term", run: () => (app.view = "ide") },
    { kind: "Action", label: "Settings", icon: "sun", run: () => (app.view = "settings") },
    { kind: "Action", label: "Analytics", icon: "chart", run: () => (app.view = "usage") },
    { kind: "Action", label: "Terminal", icon: "term", run: () => { app.openTab("terminal"); } },
  ];
  const items = $derived.by(() => {
    const ql = q.trim().toLowerCase();
    const m = (s?: string) => !ql || (s ?? "").toLowerCase().includes(ql);
    const out: Item[] = [];
    for (const a of actions) if (m(a.label) || m(t(a.label))) out.push(a);
    for (const p of app.playbooks) if (m(p.name)) out.push({ kind: "Playbook", label: "/" + p.name, icon: "book", run: () => { app.newTask(); app.quote = p.body; } });
    for (const c of app.chats) {
      if (out.length > 60) break;
      if (m(c.title) || m(c.cwd) || m(c.repo)) out.push({ kind: "Chat", label: c.title || t("Untitled"), sub: (c.cwd || c.repo || "").split("/").filter(Boolean).pop(), icon: c.kind === "cloud" ? "cloud" : "chat", run: () => app.open(c) });
    }
    return out;
  });
  $effect(() => { q; sel = 0; });
  function key(e: KeyboardEvent) {
    if (e.key === "ArrowDown") { e.preventDefault(); sel = Math.min(sel + 1, items.length - 1); }
    else if (e.key === "ArrowUp") { e.preventDefault(); sel = Math.max(sel - 1, 0); }
    else if (e.key === "Enter") { e.preventDefault(); go(sel); }
    else if (e.key === "Escape") app.palette = false;
  }
  function go(i: number) { const it = items[i]; app.palette = false; it?.run(); }
  const focus = (el: HTMLInputElement) => focusOnMount(el);
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="shade" onclick={() => (app.palette = false)}>
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="pal card" onclick={e => e.stopPropagation()}>
    <div class="qrow"><Icon name="search" size={15} /><input placeholder={t("Search chats, actions, playbooks")} bind:value={q} onkeydown={key} use:focus /></div>
    <div class="list scroll">
      {#each items as it, i}
        <button class="it" class:on={i === sel} onmouseenter={() => (sel = i)} onclick={() => go(i)}>
          <Icon name={it.icon} size={14} />
          <span class="lb">{it.kind === "Action" ? t(it.label) : it.label}</span>
          {#if it.sub}<span class="muted mono sub">{it.sub}</span>{/if}
          <span class="muted kind">{t(it.kind)}</span>
        </button>
      {:else}
        <div class="none muted">{t("Nothing found")}</div>
      {/each}
    </div>
  </div>
</div>

<style>
  .shade { position: fixed; inset: 0; z-index: 80; background: rgba(0,0,0,.18); display: grid; justify-items: center; align-items: start; padding-top: 14vh; }
  .pal { width: min(600px, 90vw); padding: 6px; display: grid; }
  .qrow { display: flex; align-items: center; gap: 10px; padding: 8px 12px 10px; border-bottom: 1px solid var(--line-soft); color: var(--muted); }
  .qrow input { flex: 1; border: 0; outline: 0; background: transparent; font-size: 15px; color: var(--ink); }
  .list { max-height: 50vh; padding-top: 4px; display: grid; }
  .it { display: flex; align-items: center; gap: 10px; border: 0; background: transparent; padding: 8px 12px; border-radius: 10px; text-align: left; font-size: 13px; color: var(--ink-2); }
  .it.on { background: var(--hover); color: var(--ink); }
  .lb { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .sub { font-size: 11px; }
  .kind { font-size: 11px; flex: none; }
  .none { padding: 14px; text-align: center; font-size: 12.5px; }
</style>
