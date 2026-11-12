<script lang="ts">
  // A reply whose text changes in place, as one streaming does (for tests: set() is the next chunk).
  import { untrack } from "svelte";
  import Markdown from "../components/Markdown.svelte";
  let { text: first }: { text: string } = $props();
  let text = $state(untrack(() => first));
  export function set(t: string) { text = t; }
</script>

<Markdown {text} />
