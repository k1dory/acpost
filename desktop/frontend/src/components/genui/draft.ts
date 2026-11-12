import { app } from "../../lib/state.svelte";

/** An answer from a reply's question or form, put into the person's message being written: in
 * place of the answer given before (if the draft still has it as it was put there), else after what
 * is there (the composer takes app.quote, as for ⌘L). Never sent from here: the person sends it. */
export function toDraft(text: string, before = "") {
  const d = app.draftOf();
  if (before && d.text.includes(before)) d.text = d.text.replace(before, () => text);
  else app.quote = (app.quote ? app.quote + "\n" : "") + text;
  if (app.view !== "chat" && app.view !== "ide") app.view = "chat";
}

/** Whether an answer is in the person's message being written (or on its way there): what was added
 * and since deleted can be added again. */
export function inDraft(text: string): boolean {
  const s = text.trim();
  return !!s && (app.draftOf().text.includes(s) || app.quote.includes(s));
}
