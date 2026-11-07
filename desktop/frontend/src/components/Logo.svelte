<script lang="ts">
  import logo from "../lib/logo.json";

  // The acpost mark (geometry from desktop/ide/build-logo.mjs), in the text colour: black on the
  // light theme, light on the dark. Drawing: its stripes run in from the left, top to bottom, and
  // make the letter; looping, it is the app's "loading".
  let { size = 28, draw = false, loop = false }: { size?: number; draw?: boolean; loop?: boolean } = $props();
  const uid = "lg" + Math.random().toString(36).slice(2, 8);
</script>

<svg class="logo" class:draw={draw || loop} class:loop width={size} height={size} viewBox={logo.view} aria-label="acpost" role="img">
  <defs><clipPath id={uid}><path clip-rule="evenodd" d={logo.clip} /></clipPath></defs>
  <g clip-path="url(#{uid})" fill="currentColor">
    {#each logo.stripes as s, i (i)}
      <rect x={logo.x0} y={s.y} width={logo.x1 - logo.x0} height={s.h} fill-opacity={s.o} style="--i:{i}" />
    {/each}
  </g>
</svg>

<style>
  .logo { display: block; color: var(--ink); flex: none; }
  .draw rect { transform-box: fill-box; transform-origin: left center; transform: scaleX(0);
    animation: run .5s cubic-bezier(.2, .7, .2, 1) forwards; animation-delay: calc(var(--i) * 45ms); }
  @keyframes run { to { transform: scaleX(1); } }
  /* Looping: drawn in, held, gone, drawn again — every stripe on the same 2.4 s beat. */
  .loop rect { animation: cycle 2.4s cubic-bezier(.2, .7, .2, 1) infinite; animation-delay: calc(var(--i) * 45ms); }
  @keyframes cycle {
    0% { transform: scaleX(0); opacity: 1; }
    28% { transform: scaleX(1); opacity: 1; }
    72% { transform: scaleX(1); opacity: 1; }
    90%, 100% { transform: scaleX(1); opacity: 0; }
  }
  @media (prefers-reduced-motion: reduce) { .draw rect, .loop rect { animation: none; transform: none; } }
</style>
