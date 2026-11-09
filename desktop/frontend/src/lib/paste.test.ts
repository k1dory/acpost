// @vitest-environment happy-dom
import { describe, it, expect } from "vitest";
import { pasteFiles, pasteNames, type Clip } from "./paste";

const img = (name = "image.png", type = "image/png") => new File([new Uint8Array([1, 2, 3])], name, { type });
const clip = (files: File[], text = "", asItems = false): Clip => ({
  files: asItems ? [] : files,
  items: asItems ? files.map(f => ({ kind: "file", type: f.type, getAsFile: () => f })) : [],
  getData: (t: string) => (t === "text/plain" ? text : ""),
});

describe("what a paste attaches", () => {
  it("a screenshot alone: attached (from files or from items, once)", () => {
    const f = img();
    expect(pasteFiles(clip([f]))).toEqual([f]);
    expect(pasteFiles(clip([f], "", true))).toEqual([f]);
    expect(pasteFiles({ files: [f], items: [{ kind: "file", type: f.type, getAsFile: () => f }], getData: () => "" })).toEqual([f]);
  });
  it("text alone: nothing, the text pastes as usual", () => {
    expect(pasteFiles(clip([], "hello"))).toEqual([]);
  });
  it("files copied in Finder (their names, or file:// addresses, as text): attached", () => {
    const a = img("a.png"), b = new File(["x"], "notes.txt", { type: "text/plain" });
    expect(pasteFiles(clip([a, b], "a.png\nnotes.txt"))).toEqual([a, b]);
    expect(pasteFiles(clip([a], "file:///Users/me/Desktop/a.png"))).toEqual([a]);
  });
  it("a part of a page with its picture: the text only, never both", () => {
    expect(pasteFiles(clip([img()], "A paragraph of the page"))).toEqual([]);
  });
});

describe("names of pasted files", () => {
  const at = new Date(2026, 10, 9, 14, 5, 9);
  it("a pasted image gets a name of its own, by its type", () => {
    const [f] = pasteNames([img("image.png")], [], at);
    expect(f.name).toMatch(/^pasted-140509-\d+\.png$/);
    expect(pasteNames([img("", "image/jpeg")], [], at)[0].name).toMatch(/\.jpg$/);
  });
  it("two pastes, two names", () => {
    const [a] = pasteNames([img()], [], at);
    const [b] = pasteNames([img()], [a.name], at);
    expect(a.name).not.toBe(b.name);
    const two = pasteNames([img(), img()], [], at);
    expect(two[0].name).not.toBe(two[1].name);
  });
  it("a real name stays, unless one attached has it", () => {
    const f = img("diagram.png");
    expect(pasteNames([f], ["other.png"], at)[0]).toBe(f);
    expect(pasteNames([f], ["diagram.png"], at)[0].name).toBe("diagram (2).png");
    expect(pasteNames([f], ["diagram.png", "diagram (2).png"], at)[0].name).toBe("diagram (3).png");
  });
});
