// @vitest-environment happy-dom
import { describe, it, expect } from "vitest";
import { pasteFiles, pasteName, freeName, type Clip } from "./paste";

const img = (name = "image.png", type = "image/png") => new File([new Uint8Array([1, 2, 3])], name, { type });
const clip = (files: File[], text = "", asItems = false): Clip => ({
  files: asItems ? [] : files,
  items: asItems ? files.map(f => ({ kind: "file", type: f.type, getAsFile: () => f })) : [],
  getData: (t: string) => (t === "text/plain" ? text : ""),
});

describe("what a paste attaches", () => {
  it("a screenshot alone: attached (from files, or from items when files is empty; once)", () => {
    const f = img();
    expect(pasteFiles(clip([f]))).toEqual([f]);
    expect(pasteFiles(clip([f], "", true))).toEqual([f]);
    expect(pasteFiles({ files: [f], items: [{ kind: "file", type: f.type, getAsFile: () => f }], getData: () => "" })).toEqual([f]);
  });
  it("two files that say the same of themselves are both kept (R1.2)", () => {
    const at = 1_700_000_000_000;
    const one = new File(["one"], "report.txt", { type: "text/plain", lastModified: at });
    const two = new File(["two"], "report.txt", { type: "text/plain", lastModified: at });
    expect(pasteFiles(clip([one, two]))).toEqual([one, two]);
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
  it("names compared exactly: spaces and empty lines make it text, one ending line break is Finder's (R2)", () => {
    expect(pasteFiles(clip([img()], "  image.png\n\n"))).toEqual([]);
    expect(pasteFiles(clip([img()], "image.png\n\n"))).toEqual([]);
    expect(pasteFiles(clip([img()], " "))).toEqual([]);
    const spaced = new File(["x"], " report.txt", { type: "text/plain" });
    expect(pasteFiles(clip([spaced], " report.txt"))).toEqual([spaced]);
    const a = img("a.png"), b = img("b.png");
    expect(pasteFiles(clip([a], "a.png\n"))).toEqual([a]);
    expect(pasteFiles(clip([a, b], "a.png\r\nb.png\r\n"))).toEqual([a, b]);
  });

  it("text that only ends in the file's name is text: an address, a path in a sentence (R1.3)", () => {
    expect(pasteFiles(clip([img()], "https://example.com/image.png"))).toEqual([]);
    expect(pasteFiles(clip([img()], "See assets/image.png"))).toEqual([]);
    expect(pasteFiles(clip([img()], "file:not a url/image.png x"))).toEqual([]);
  });
});

describe("names of pasted files", () => {
  const at = new Date(2026, 10, 9, 14, 5, 9);
  it("a pasted picture with WebKit's name, or a file with none, gets a name of its own by its type", () => {
    expect(pasteName({ name: "image.png", type: "image/png" }, [], at)).toMatch(/^pasted-140509-\d+\.png$/);
    expect(pasteName({ name: "", type: "image/jpeg" }, [], at)).toMatch(/\.jpg$/);
    expect(pasteName({ name: "", type: "text/plain" }, [], at)).toMatch(/^pasted-.*\.txt$/);
  });
  it("two pastes, two names", () => {
    const a = pasteName({ name: "image.png", type: "image/png" }, [], at);
    const b = pasteName({ name: "image.png", type: "image/png" }, [a], at);
    expect(a).not.toBe(b);
  });
  it("a real name stays, unless one attached has it", () => {
    expect(pasteName({ name: "diagram.png", type: "image/png" }, ["other.png"], at)).toBe("diagram.png");
    expect(pasteName({ name: "diagram.png", type: "image/png" }, ["diagram.png"], at)).toBe("diagram (2).png");
    expect(freeName("diagram.png", ["diagram.png", "diagram (2).png"])).toBe("diagram (3).png");
  });
  it("dotfiles and a file named like a picture that is not one keep their names (R1.4)", () => {
    expect(pasteName({ name: ".env", type: "text/plain" }, [], at)).toBe(".env");
    expect(pasteName({ name: ".gitignore", type: "" }, [], at)).toBe(".gitignore");
    expect(pasteName({ name: "image.txt", type: "text/plain" }, [], at)).toBe("image.txt");
    expect(freeName(".env", [".env"])).toBe(".env (2)");
  });
});
