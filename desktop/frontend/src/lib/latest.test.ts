import { describe, it, expect } from "vitest";
import { latest } from "./latest";
import { deferred } from "../test/fake";

describe("latest (F7/Y12)", () => {
  it("applies only the last call's answer when two answer out of order", async () => {
    const run = latest();
    const a = deferred<string>(), b = deferred<string>();
    let shown = "";
    const ra = run(() => a.promise, v => (shown = v));
    const rb = run(() => b.promise, v => (shown = v));
    b.resolve("7 days");
    expect(await rb).toBe(true);
    a.resolve("all");
    expect(await ra).toBe(false);
    expect(shown).toBe("7 days");
  });
  it("drops an earlier call's error too", async () => {
    const run = latest();
    const a = deferred<string>();
    let err = "";
    const ra = run(() => a.promise, () => {}, e => (err = String(e)));
    await run(async () => "ok", () => {});
    a.reject("old");
    await ra;
    expect(err).toBe("");
  });
});
