import { describe, it, expect } from "vitest";
import { b64, b64bytes, unb64 } from "./base64";

describe("base64", () => {
  it("round-trips text as UTF-8", () => expect(new TextDecoder().decode(unb64(b64("привет ✓")))).toBe("привет ✓"));
  it("encodes bytes past one chunk", () => {
    const b = new Uint8Array(0x8000 * 2 + 5).map((_, i) => i % 251);
    expect(unb64(b64bytes(b))).toEqual(b);
  });
});
