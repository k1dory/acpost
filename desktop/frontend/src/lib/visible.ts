/** A Svelte action: tells, as the element comes into view (or near it) and leaves it. Where there is
 * no IntersectionObserver it is in view at once. Opening a long thread then asks only for what is
 * on screen, not for every message's subtasks. */
export function inView(el: Element, cb: (visible: boolean) => void) {
  if (typeof IntersectionObserver === "undefined") { cb(true); return {}; }
  const io = new IntersectionObserver(es => { for (const e of es) cb(e.isIntersecting); }, { rootMargin: "300px 0px" });
  io.observe(el);
  return {
    update(next: (visible: boolean) => void) { cb = next; },
    destroy() { io.disconnect(); },
  };
}
