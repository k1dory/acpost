import { describe, it, expect, vi, afterEach } from "vitest";
import { inView } from "./visible";

// An IntersectionObserver the test drives: which elements are on screen.
class FakeIO {
  static all: FakeIO[] = [];
  el: Element | null = null;
  constructor(public cb: (es: { isIntersecting: boolean }[]) => void) { FakeIO.all.push(this); }
  observe(el: Element) { this.el = el; }
  disconnect() { this.el = null; }
  show(v: boolean) { this.cb([{ isIntersecting: v }]); }
}

afterEach(() => { vi.unstubAllGlobals(); FakeIO.all = []; });

describe("inView (F5)", () => {
  it("of fifty messages, only those on screen are asked for", () => {
    vi.stubGlobal("IntersectionObserver", FakeIO);
    const load = vi.fn();
    for (let i = 0; i < 50; i++) inView({} as Element, v => v && load(i));
    expect(load).not.toHaveBeenCalled();
    for (const io of FakeIO.all.slice(-3)) io.show(true); // the last three, at the bottom
    expect(load.mock.calls.map(c => c[0])).toEqual([47, 48, 49]);
  });
  it("says when it leaves the screen, and stops when destroyed", () => {
    vi.stubGlobal("IntersectionObserver", FakeIO);
    const seen: boolean[] = [];
    const a = inView({} as Element, v => seen.push(v));
    FakeIO.all[0].show(true); FakeIO.all[0].show(false);
    a.destroy?.();
    expect(seen).toEqual([true, false]);
    expect(FakeIO.all[0].el).toBeNull();
  });
  it("is in view at once with no IntersectionObserver", () => {
    vi.stubGlobal("IntersectionObserver", undefined);
    const cb = vi.fn();
    inView({} as Element, cb);
    expect(cb).toHaveBeenCalledWith(true);
  });
});
