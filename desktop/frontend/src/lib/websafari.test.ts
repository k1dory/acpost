// The app runs in the system's WebView, as old as macOS 12's (Safari 15): a regular expression with
// a lookbehind does not parse there, and the module holding it — the whole interface — fails to
// load. No source of the app has one (review round 2 of PR #26).
import { describe, it, expect } from "vitest";

const raw = import.meta.glob(["../**/*.svelte", "../**/*.ts", "../**/*.js", "!../**/*.test.ts", "!../test/**"], { query: "?raw", import: "default", eager: true }) as Record<string, string>;

describe("syntax the oldest WebView parses", () => {
  it("has no regex lookbehind in any source", () => {
    const files = Object.keys(raw);
    expect(files.length).toBeGreaterThan(20);
    expect(files.some(f => /(^|\/)brand\.ts$/.test(f))).toBe(true);
    const found = files.filter(f => /\(\?<[=!]/.test(raw[f]));
    expect(found).toEqual([]);
  });
});
