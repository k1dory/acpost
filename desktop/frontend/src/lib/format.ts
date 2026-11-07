import { t, i18n } from "./i18n.svelte";

const i18nLocale = () => (i18n.lang === "ru" ? "ru-RU" : undefined);

export function dur(ms: number): string {
  if (!isFinite(ms) || ms < 0) return "";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)}s`;
  if (ms < 3_600_000) return `${Math.round(ms / 60_000)}m`;
  return `${(ms / 3_600_000).toFixed(1)}h`;
}

export function tokens(n: number): string {
  if (!n) return "0";
  if (n < 1000) return String(n);
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`;
  return `${(n / 1_000_000).toFixed(2)}M`;
}

export function money(v: number, cur = "USD"): string {
  if (cur === "mixed") return `${v.toFixed(2)} (${t("mixed")})`;
  try {
    return new Intl.NumberFormat(undefined, { style: "currency", currency: cur || "USD", maximumFractionDigits: v < 1 ? 3 : 2 }).format(v);
  } catch {
    return `${v.toFixed(2)} ${cur}`;
  }
}

export function ago(iso: string | Date): string {
  const at = typeof iso === "string" ? Date.parse(iso) : iso.getTime();
  if (!at) return "";
  const s = (Date.now() - at) / 1000;
  if (s < 45) return t("now");
  if (s < 3600) return t("{n}m", { n: Math.round(s / 60) });
  if (s < 86400) return t("{n}h", { n: Math.round(s / 3600) });
  if (s < 7 * 86400) return t("{n}d", { n: Math.round(s / 86400) });
  return new Date(at).toLocaleDateString(i18nLocale());
}

export function clock(iso: string): string {
  const at = Date.parse(iso);
  return at ? new Date(at).toLocaleTimeString(i18nLocale(), { hour: "2-digit", minute: "2-digit" }) : "";
}

/** agent:name@node → name (node) */
export function who(addr: string): { name: string; node: string; kind: string } {
  const [kind, rest = ""] = addr.includes(":") ? addr.split(/:(.*)/s) : ["agent", addr];
  const [name, node = ""] = rest.split("@");
  return { kind, name, node };
}

export function initials(name: string): string {
  const p = name.replace(/[^A-Za-z0-9а-яА-Я]+/g, " ").trim().split(" ");
  return ((p[0]?.[0] ?? "?") + (p[1]?.[0] ?? "")).toUpperCase();
}

/** A stable colour for an agent's badge. */
export function hue(name: string): number {
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) % 360;
  return h;
}

/** How much the page's own coordinates are scaled on screen (the app's zoom, as the engine applies
 * it): a measured box at left 100 is at 100 × this. Screen coordinates (a click, a box's rect)
 * divided by it are what fixed positions take. */
export function screenScale(): number {
  const el = document.createElement("div");
  el.style.cssText = "position:fixed;left:100px;top:0;width:1px;height:1px;visibility:hidden";
  document.body.appendChild(el);
  const r = el.getBoundingClientRect().left / 100;
  el.remove();
  return r > 0 ? r : 1;
}
