<script lang="ts">
  // Line icons, 16px, stroke currentColor.
  let { name, size = 16 }: { name: string; size?: number } = $props();
  const paths: Record<string, string> = {
    plus: "M8 3v10M3 8h10",
    search: "M7 12A5 5 0 1 0 7 2a5 5 0 0 0 0 10Zm7 2-3.5-3.5",
    chat: "M3 4.5A1.5 1.5 0 0 1 4.5 3h7A1.5 1.5 0 0 1 13 4.5v5A1.5 1.5 0 0 1 11.5 11H7l-3 2.5V11h.5",
    bot: "M5 6h6a2 2 0 0 1 2 2v3a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2Zm3-3v3M6.5 9.5h0M9.5 9.5h0",
    cloud: "M4.5 12.5h7a2.5 2.5 0 0 0 .3-5A4 4 0 0 0 4.1 7 2.75 2.75 0 0 0 4.5 12.5Z",
    chart: "M3 13V7m4 6V3m4 10V9m-9 4h12",
    book: "M3 3.5A1.5 1.5 0 0 1 4.5 2H13v10H4.5A1.5 1.5 0 0 0 3 13.5v-10Zm0 10A1.5 1.5 0 0 0 4.5 15H13v-3",
    sun: "M8 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6Zm0-9v1.5m0 9V14M2 8h1.5m9 0H14M3.8 3.8l1 1m6.4 6.4 1 1m0-8.4-1 1m-6.4 6.4-1 1",
    moon: "M13 9.5A5.5 5.5 0 0 1 6.5 3a5.5 5.5 0 1 0 6.5 6.5Z",
    panel: "M3 3h10v10H3zM10 3v10",
    send: "M3 8h9M8.5 4.5 12 8l-3.5 3.5",
    stop: "M5 5h6v6H5z",
    clip: "M10.5 5.5 6 10a1.4 1.4 0 0 0 2 2l5-5a2.8 2.8 0 0 0-4-4l-5 5a4.2 4.2 0 0 0 6 6l4-4",
    check: "M3.5 8.5 6.5 11.5 12.5 4.5",
    x: "M4 4l8 8M12 4l-8 8",
    chev: "M6 4l4 4-4 4",
    term: "M3 4h10v8H3zM5.5 6.5 7 8l-1.5 1.5M8.5 10h2",
    diff: "M5 3v6m-3-3h6M9 13h5M11 3a2 2 0 1 0 0 .01",
    pr: "M5 4.5a1.5 1.5 0 1 0 0-.01M5 6v6m0 1.5a1.5 1.5 0 1 0 0-.01M11 13.5a1.5 1.5 0 1 0 0-.01M11 12V6.5A2.5 2.5 0 0 0 8.5 4H7",
    fork: "M5 3.5a1.5 1.5 0 1 0 0-.01M11 3.5a1.5 1.5 0 1 0 0-.01M8 13.5a1.5 1.5 0 1 0 0-.01M5 5v1.5A2 2 0 0 0 7 8.5h2a2 2 0 0 0 2-2V5M8 8.5V12",
    activity: "M2 8h3l2-5 2 10 2-5h3",
    eye: "M1.5 8S4 3.5 8 3.5 14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8Zm6.5 2a2 2 0 1 0 0-4 2 2 0 0 0 0 4Z",
    refresh: "M13 8a5 5 0 1 1-1.5-3.5M13 3v2.5h-2.5",
    copy: "M5.5 5.5h7v7h-7zM3.5 10.5v-7h7",
    file: "M4 2h5l3 3v9H4zM9 2v3h3",
    alert: "M8 2 14.5 13.5h-13L8 2Zm0 4.5v3m0 2h0",
    clock: "M8 14A6 6 0 1 0 8 2a6 6 0 0 0 0 12Zm0-9v3.5L10 10",
    brain: "M6 3.5A2 2 0 0 0 3.5 6 2 2 0 0 0 3 9.5 2.5 2.5 0 0 0 6 13V3.5Zm4 0A2 2 0 0 1 12.5 6a2 2 0 0 1 .5 3.5A2.5 2.5 0 0 1 10 13V3.5Z",
  };
</script>

<svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
  <path d={paths[name] ?? paths.chat} />
</svg>
