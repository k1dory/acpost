// Errors as the node says them ({code, message, hint}), said to a person. Apart from api.ts so
// what uses them does not load the bindings (tests mock those).
import { t, tr } from "./i18n.svelte";

/** An error as the node said it: its code, message and what to do next. */
export type ErrInfo = { code: string; message: string; hint: string };

export function errInfo(e: unknown): ErrInfo {
  if (!e) return { code: "", message: "", hint: "" };
  let m: string = typeof e === "string" ? e : (e as any).message ?? String(e);
  // Wails wraps the Go error's text as JSON; the desktop's text is JSON itself: unwrap both.
  for (let i = 0; i < 3; i++) {
    try {
      const j = JSON.parse(m);
      if (j && typeof j === "object") {
        if (j.code !== undefined || j.hint !== undefined) return { code: j.code ?? "", message: j.message ?? "", hint: j.hint ?? "" };
        if (typeof j.message === "string") { m = j.message; continue; }
      }
    } catch {}
    break;
  }
  return { code: "", message: m, hint: "" };
}

// What each kind of failure is, said to a person.
const titles: Record<string, string> = {
  invalid: "Check what was entered",
  not_found: "Not found",
  refused: "Not allowed",
  conflict: "It changed meanwhile",
  timeout: "No answer in time",
  unavailable: "Not reachable right now",
  unsupported: "Not supported here",
  node_down: "The acpost node is not running",
  session_busy: "The session is open in another app",
  session_not_found: "That session no longer exists",
  agent_error: "The agent reported an error",
  agent_start: "The agent did not start",
  agent_auth: "The agent needs you to sign in",
};

/** An error for a person: what happened (in the app's language), the detail, what to do. */
export function errText(e: unknown, title?: string): string {
  const x = errInfo(e);
  if (!x.message && !x.code) return "";
  const head = title ? t(title) : titles[x.code] ? t(titles[x.code]) : "";
  const msg = tr(x.message);
  const parts = [head && msg ? `${head}: ${msg}` : head || msg];
  if (x.hint) parts.push(t("What to do") + ": " + tr(x.hint));
  return parts.join("\n");
}
