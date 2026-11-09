// The interface says nothing in English outside t(): text in the templates, and the keys t() is
// given, all have their Russian (F8). The sources are read as text (Vite's raw imports).
import { describe, it, expect } from "vitest";
import { ru } from "./ru";

const raw = import.meta.glob(["../**/*.svelte", "../**/*.ts", "!../**/*.test.ts", "!../test/**"], { query: "?raw", import: "default", eager: true }) as Record<string, string>;
const all = Object.keys(raw);
// Names that stay as they are in any language.
const brands = /^(acpost forge login|Claude Code|Codex|VS Code|IDE \(VS Code\)|acpost|MCP|HTTP|NAME|PR|CI|URL|ACP|Git|GitHub|GitLab|Forgejo|Gitea|JSON|stdio)$/;

/** A template with its code, styles, comments and {expressions} taken out: what is left is text. */
function templateText(s: string): string[] {
  s = s.replace(/<script[\s\S]*?<\/script>/g, "").replace(/<style[\s\S]*?<\/style>/g, "").replace(/<!--[\s\S]*?-->/g, "");
  let out = "", depth = 0;
  for (const c of s) { if (c === "{") depth++; else if (c === "}") depth = Math.max(0, depth - 1); else if (!depth) out += c; }
  const found: string[] = [];
  // Attributes people read.
  for (const m of out.matchAll(/\b(title|placeholder|aria-label|alt)="([^"]*[A-Za-z]{2,}[^"]*)"/g)) found.push(m[2]);
  for (const m of out.replace(/<[^>]*>/g, "\n").matchAll(/[^\n]*[A-Za-z]{2,}[^\n]*/g)) found.push(m[0].trim());
  return found.filter(x => x && !brands.test(x));
}

describe("i18n (F8)", () => {
  it("templates have no English text outside t()", () => {
    expect(all.filter(f => f.endsWith(".svelte")).length).toBeGreaterThan(30); // it does read them
    const hits: string[] = [];
    for (const f of all.filter(f => f.endsWith(".svelte"))) {
      for (const x of templateText(raw[f])) hits.push(`${f}: ${x}`);
    }
    expect(hits).toEqual([]);
  });

  it("every text given to t() has its Russian", () => {
    const missing = new Set<string>();
    for (const f of all) {
      if (f.endsWith("i18n.svelte.ts")) continue; // its comment shows t("English text")
      const s = raw[f];
      for (const m of s.matchAll(/\bt\(\s*"((?:[^"\\]|\\.)*)"/g)) {
        const k = JSON.parse(`"${m[1]}"`);
        if (!(k in ru) && !brands.test(k)) missing.add(k);
      }
    }
    expect([...missing]).toEqual([]);
  });
});
