import { app } from "../../lib/state.svelte";

/** Where an answer is in a text: whole, its first line a line of the text from its start and its
 * last line one to its end ("Java" is not in "JavaScript", nor "1" in "10"); -1 when it is not. */
/** An answer as it stands in a draft: without the newlines around it; the spaces of its own lines
 * (a field left blank ends "- Notes: ") are part of it. */
const whole = (text: string) => text.replace(/^\n+|\n+$/g, "");

export function answerAt(hay: string, text: string): number {
  const s = whole(text);
  if (!s) return -1;
  for (let i = hay.indexOf(s); i >= 0; i = hay.indexOf(s, i + 1)) {
    const end = i + s.length;
    if ((i === 0 || hay[i - 1] === "\n") && (end === hay.length || hay[end] === "\n")) return i;
  }
  return -1;
}

/** An answer from a reply's question or form, put into the person's message being written: in
 * place of the answer given before (if the draft still has it whole, as it was put there), else
 * after what is there (the composer takes app.quote, as for ⌘L). Never sent from here: the person
 * sends it. */
export function toDraft(text: string, before = "") {
  const d = app.draftOf();
  const at = before ? answerAt(d.text, before) : -1;
  if (at >= 0) d.text = d.text.slice(0, at) + whole(text) + d.text.slice(at + whole(before).length);
  else app.quote = (app.quote ? app.quote + "\n" : "") + text;
  if (app.view !== "chat" && app.view !== "ide") app.view = "chat";
}

/** Whether an answer is in the person's message being written (or on its way there), whole: what
 * was added and since deleted, or another answer that only starts like it, can be added. */
export function inDraft(text: string): boolean {
  return answerAt(app.draftOf().text, text) >= 0 || answerAt(app.quote, text) >= 0;
}
