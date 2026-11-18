// Whether a chat follows its end as it grows, as a messenger does: it does while the person is at
// the end, stops when they scroll up to read, and does again once they are back at the end.

/** How near the end (px) still counts as being at it. */
export const nearEnd = 80;

/** After a scroll: `gap` is what is left below the view, `top` where it is now, `was` where it was
 * before. Content growing under a view at its end leaves a gap without anyone scrolling, so a gap
 * alone says nothing: a move up is the person leaving the end (however little: text that streams
 * in must not pull them back), a move down to near the end is them back at it. */
export function follows(stick: boolean, gap: number, top: number, was: number): boolean {
  if (gap <= 2) return true;           // at the very end (content that shrank leaves the view there)
  if (top < was - 1) return false;     // moved up
  if (top > was + 1 && gap < nearEnd) return true;
  return stick;
}
