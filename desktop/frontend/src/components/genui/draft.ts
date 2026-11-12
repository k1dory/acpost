import { app } from "../../lib/state.svelte";

/** An answer from a reply's question or form, put into the person's message being written (the
 * composer takes app.quote, as for ⌘L): never sent from here, the person sends it. */
export function toDraft(text: string) {
  app.quote = (app.quote ? app.quote + "\n" : "") + text;
  if (app.view !== "chat" && app.view !== "ide") app.view = "chat";
}
