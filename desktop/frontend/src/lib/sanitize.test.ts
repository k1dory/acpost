import { describe, it, expect } from "vitest";
import { loadsRemote, pageSandbox } from "./sanitize";
import { csp } from "./csp";

describe("what an answer may load (F6)", () => {
  it("drops pictures from the web", () => {
    expect(loadsRemote("IMG", "src", "http://evil.example/p.gif?chat=1")).toBe(true);
    expect(loadsRemote("img", "src", "//evil.example/p.gif")).toBe(true);
    expect(loadsRemote("img", "srcset", "a.png 1x, https://evil.example/b.png 2x")).toBe(true);
    expect(loadsRemote("div", "style", "background:url('https://evil.example/x')")).toBe(true);
    expect(loadsRemote("image", "xlink:href", "https://evil.example/x.svg")).toBe(true);
  });
  it("keeps links and what is the app's own", () => {
    expect(loadsRemote("a", "href", "https://example.com")).toBe(false);
    expect(loadsRemote("img", "src", "data:image/png;base64,AAAA")).toBe(false);
    expect(loadsRemote("img", "alt", "http://x")).toBe(false);
  });
});

describe("artifact pages (F6)", () => {
  it("run no scripts unless allowed", () => {
    expect(pageSandbox(false)).toBe("");
    expect(pageSandbox(true)).toBe("allow-scripts");
  });
});

describe("the app's policy (F6)", () => {
  const dir = (p: string, name: string) => p.split(";").map(s => s.trim()).find(s => s.startsWith(name + " "))?.split(/\s+/).slice(1) ?? [];
  it("lets pictures and connections reach only the app", () => {
    const p = csp();
    expect(dir(p, "img-src")).toEqual(["'self'", "data:", "blob:"]);
    expect(dir(p, "connect-src")).toEqual(["'self'", "wails:"]);
    for (const d of ["default-src", "img-src", "connect-src", "frame-src", "font-src", "media-src"])
      expect(dir(p, d).some(s => /^(https?:|\*|ws)/.test(s))).toBe(false);
    expect(dir(p, "form-action")).toEqual(["'none'"]);
  });
  it("adds only the dev server's socket in dev", () => {
    expect(dir(csp(true, 9245), "connect-src")).toEqual(["'self'", "wails:", "ws://127.0.0.1:9245", "http://127.0.0.1:9245"]);
  });
});
