<script lang="ts">
  import { app } from "../lib/state.svelte";
  import { tick } from "svelte";
  import Message from "./Message.svelte";
  import Composer from "./Composer.svelte";
  import Icon from "./Icon.svelte";

  let box: HTMLDivElement;
  let stick = true;
  const conv = $derived(app.convs.find(c => c.thread === app.thread));
  const title = $derived(conv?.title || (app.msgs[0]?.body ?? "").split("\n")[0].slice(0, 80) || "New task");
  const waiting = $derived((app.rev, Object.entries(app.turns).flatMap(([id, t]) => t.waiting().map(r => ({ id, r })))));

  function onScroll() { stick = box.scrollHeight - box.scrollTop - box.clientHeight < 80; }
  $effect(() => { app.rev; app.msgs.length; if (stick) tick().then(() => box && (box.scrollTop = box.scrollHeight)); });
</script>

<section class="conv">
  <header class="drag">
    <div class="title">{title}</div>
    {#if waiting.length}<span class="chip ask nodrag">{waiting.length} needs approval</span>{/if}
    <span class="sp"></span>
    <button class="btn ghost sm nodrag" title="Right panel (⌘\)" onclick={() => (app.panel = !app.panel)}><Icon name="panel" size={14} /></button>
  </header>

  <div class="scroll body" bind:this={box} onscroll={onScroll}>
    <div class="col">
      {#if !app.thread}
        <div class="hello">
          <div class="big">What should we work on?</div>
          <div class="muted">Pick an agent below — on this machine, on another node, or in the cloud.</div>
        </div>
      {/if}
      {#each app.msgs as m (m.id)}<Message {m} />{/each}
    </div>
  </div>

  <div class="foot"><div class="col"><Composer /></div></div>
</section>

<style>
  .conv { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--page); }
  header { height: 52px; flex: none; display: flex; align-items: center; gap: 10px; padding: 0 16px 0 22px; border-bottom: 1px solid var(--line-soft); }
  .title { font-weight: 600; font-size: 15px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .sp { flex: 1; }
  .body { flex: 1; min-height: 0; padding: 18px 22px 8px; }
  .col { max-width: 780px; margin: 0 auto; }
  .foot { padding: 6px 22px 18px; }
  .hello { padding: 18vh 0 24px; display: grid; gap: 6px; }
  .big { font-size: 24px; font-weight: 600; letter-spacing: -.01em; }
</style>
