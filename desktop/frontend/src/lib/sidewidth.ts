// The width of the chat's column beside the IDE or a module.

export const sideDefault = 400;
/** Narrower than this a message's tools and the composer's options do not fit. */
export const sideMin = 320;
/** What is left to the view it stands beside, at the least. */
export const sideRest = 360;

/** `w` as the column can be in a window `win` wide (both in the page's pixels). */
export function sideWidth(w: number, win: number): number {
  const max = Math.max(sideMin, (Number.isFinite(win) ? win : 0) - sideRest); // a window not measured: the least
  return Math.round(Math.max(sideMin, Math.min(max, w || sideDefault)));
}
