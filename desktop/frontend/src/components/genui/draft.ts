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
