import { describe, it, expect } from "vitest";
import { afterSent, overLimits } from "./draft";

describe("afterSent (F1/Y2)", () => {
  it("empties the composer when it holds what was sent", () => expect(afterSent("hi", "hi")).toBe(""));
  it("keeps what was typed on while it went", () => expect(afterSent("hi\nand more", "hi")).toBe("and more"));
  it("keeps a text changed meanwhile as it is", () => expect(afterSent("ho", "hi")).toBe("ho"));
});

describe("overLimits (F23)", () => {
  const lim = { max_files: 2, max_files_size: 100 };
  it("lets a file within the limits", () => expect(overLimits([{ size: 10 }], { size: 50 }, lim)).toBe(""));
  it("refuses one file too many", () => expect(overLimits([{ size: 1 }, { size: 1 }], { size: 1 }, lim)).toBe("count"));
  it("refuses files too big together", () => expect(overLimits([{ size: 60 }], { size: 50 }, lim)).toBe("size"));
  it("lets anything when the limits are not known", () => expect(overLimits([], { size: 1e12 }, null)).toBe(""));
});
