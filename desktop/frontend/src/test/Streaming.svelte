<script lang="ts">
  // A reply whose text changes in place, as one streaming does (for tests: set() is the next chunk),
  // or a page shown in the same Markdown as another (set() with another scope).
  import { untrack } from "svelte";
  import Markdown from "../components/Markdown.svelte";
  let { text: first, scope: firstScope = "" }: { text: string; scope?: string } = $props();
  let text = $state(untrack(() => first));
  let scope = $state(untrack(() => firstScope));
  export function set(t: string, sc?: string) { text = t; if (sc !== undefined) scope = sc; }
</script>

<Markdown {text} {scope} />
