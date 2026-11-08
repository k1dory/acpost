import { describe, it, expect } from "vitest";
import { usageTotal, costText } from "./usage";

const row = (o: any) => ({ key: "", turns: 1, input: 0, output: 0, cache_read: 0, cache_write: 0, ...o });

describe("usage total (Y21)", () => {
  it("sums costs per currency, never as one", () => {
    const t = usageTotal([row({ cost: 1, currency: "USD" }), row({ cost: 1, currency: "EUR" })]);
    expect(t.costs).toEqual({ USD: 1, EUR: 1 });
    const s = costText(t.costs);
    expect(s).toContain("€");
    expect(s).toContain("$");
  });
  it("counts an estimate in its own currency", () => {
    const t = usageTotal([row({ cost: 2, currency: "USD", estimated: 0.5, estimated_currency: "EUR" })]);
    expect(t.costs).toEqual({ USD: 2, EUR: 0.5 });
  });
  it("sums the same currency", () => {
    expect(usageTotal([row({ cost: 1, currency: "USD" }), row({ estimated: 2, estimated_currency: "USD" })]).costs).toEqual({ USD: 3 });
  });
});
