<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { app, type ArtifactRef } from "../lib/state.svelte";
  import Icon from "./Icon.svelte";
  import ArtifactView from "./ArtifactView.svelte";
  import { refKey, sameRef } from "../lib/artifactref";

  // Artifacts: the files the agents handed back in this chat (attached to their replies) and the
  // files of this Mac a reply linked to, the one picked shown by ArtifactView.
  const attached = $derived(app.msgs.flatMap(m => (m.files ?? []).filter(f => f.reply).map(f => ({ msg: m.id, name: f.name, base: app.chatCwd() }) as ArtifactRef)));
  const all = $derived.by(() => {
    const out = [...attached];
    for (const a of app.openedArtifacts) if (!out.some(x => sameRef(x, a))) out.push(a);
    return out;
  });
  const cur = $derived(app.artifact ?? all[0] ?? null);
  const label = (a: ArtifactRef) => a.name || a.path || "";
</script>

<div class="af">
  {#if !all.length}
    <div class="empty">{t("Files the agents hand back, and files of this Mac a reply links to, show here.")}</div>
  {:else}
    <div class="list">
      {#each all as a (refKey(a))}
        <button class="it" class:on={sameRef(cur, a)} onclick={() => (app.artifact = a)} title={a.path ?? a.name}>
          <Icon name="file" size={12} /><span>{label(a).split("/").pop()}</span>
        </button>
      {/each}
    </div>
    <ArtifactView {cur} />
  {/if}
</div>

<style>
  .af { height: 100%; display: flex; flex-direction: column; min-height: 0; }
  .list { display: flex; gap: 4px; flex-wrap: wrap; padding: 8px; border-bottom: 1px solid var(--line-soft); max-height: 96px; overflow: auto; }
  .it { display: inline-flex; align-items: center; gap: 5px; border: 1px solid var(--line-soft); background: transparent; border-radius: 8px; padding: 3px 8px; font-size: 12px; color: var(--ink-2); max-width: 220px; }
  .it span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .it.on { background: var(--sel); color: var(--ink); border-color: transparent; }
</style>
