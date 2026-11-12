// What the Analytics view shows: its tab, and in Traces the conversation and turn opened. Kept
// apart from the app's state: a chat opens a turn's trace from here (openTrace).
import { app } from "./state.svelte";

export type AnalyticsTab = "usage" | "traces" | "commands";

class Analytics {
  tab = $state<AnalyticsTab>("usage");
  /** The conversation opened in Traces ("" for the list). */
  thread = $state("");
  /** The turn whose timeline is shown ("" for the conversation's turns). */
  turn = $state("");
}

export const analytics = new Analytics();

/** Opens a turn's timeline (from a chat: the message's agent header). */
export function openTrace(thread: string, turn: string) {
  analytics.tab = "traces";
  analytics.thread = thread;
  analytics.turn = turn;
  app.view = "usage";
}
