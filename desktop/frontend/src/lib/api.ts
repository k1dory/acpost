import { t, tr } from "./i18n.svelte";
export * as Node from "../../bindings/github.com/k1dory/acpost/desktop/node.js";
import { Events } from "@wailsio/runtime";

export function on(name: string, fn: (data: any) => void): () => void {
  return Events.On(name, (e: any) => fn(e.data));
}

export function b64(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let bin = "";
  for (const x of bytes) bin += String.fromCharCode(x);
  return btoa(bin);
}

export function unb64(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

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
