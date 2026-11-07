// The app's languages: English (the text in the code) and Russian. t("English text") gives the
// text in the chosen language; {name} in it is filled from vars. A text with no translation stays
// English.
import { ru, ruPatterns } from "./ru";

export type Lang = "auto" | "en" | "ru";

class I18n {
  choice = $state<Lang>("auto");
  constructor() {
    try { const v = localStorage.getItem("lang"); if (v === "en" || v === "ru" || v === "auto") this.choice = v; } catch {}
  }
  /** The language in effect: the chosen one, or the system's. */
  get lang(): "en" | "ru" {
    if (this.choice !== "auto") return this.choice;
    return (navigator.language || "en").toLowerCase().startsWith("ru") ? "ru" : "en";
  }
  set(l: Lang) {
    this.choice = l;
    try { localStorage.setItem("lang", l); } catch {}
    document.documentElement.lang = this.lang;
  }
}

export const i18n = new I18n();

export function t(s: string, vars?: Record<string, string | number>): string {
  let out = i18n.lang === "ru" ? ru[s] ?? s : s;
  if (vars) for (const [k, v] of Object.entries(vars)) out = out.replaceAll("{" + k + "}", String(v));
  return out;
}

/** n with the word for it: plural(3, "chat", "chats") — in Russian by its own rules. */
export function plural(n: number, one: string, many: string): string {
  if (i18n.lang !== "ru") return `${n} ${t(n === 1 ? one : many)}`;
  const forms = (ru["#" + one] ?? `${t(one)}|${t(many)}|${t(many)}`).split("|");
  const m10 = n % 10, m100 = n % 100;
  const f = m10 === 1 && m100 !== 11 ? forms[0] : m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14) ? forms[1] : forms[2];
  return `${n} ${f}`;
}

/** A message from the node (English), in the app's language where it has a known form. */
export function tr(s: string): string {
  if (i18n.lang !== "ru" || !s) return s;
  if (ru[s]) return ru[s];
  for (const [re, to] of ruPatterns) if (re.test(s)) return s.replace(re, to);
  return s;
}
