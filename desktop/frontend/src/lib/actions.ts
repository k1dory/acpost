/** A Svelte action: the field takes the focus once shown (select: its text selected too). */
export function focusOnMount(el: HTMLInputElement, select = false) {
  requestAnimationFrame(() => { el.focus(); if (select) el.select(); });
}
