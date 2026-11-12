import { describe, it, expect } from "vitest";
import { chatTree } from "./chattree";

describe("chats an agent started are listed under the chat they came from", () => {
  const c = (key: string, parent?: string) => ({ key, parent });
  it("nests each under its parent, to any depth, in the list's order", () => {
    const chats = [c("a"), c("b", "a"), c("x"), c("d", "b"), c("e", "a")];
    const { kids, nested } = chatTree(chats);
    expect([...nested]).toEqual(["b", "d", "e"]);
    expect(kids.get("a")!.map(k => k.key)).toEqual(["b", "e"]);
    expect(kids.get("b")!.map(k => k.key)).toEqual(["d"]);
  });
  it("a chat whose parent is not listed, or in a loop, is a chat like any; a filter lists flat", () => {
    expect(chatTree([c("b", "gone")]).nested.size).toBe(0);
    expect(chatTree([c("a", "b"), c("b", "a")]).nested.size).toBe(0);
    expect(chatTree([c("a", "a")]).nested.size).toBe(0);
    expect(chatTree([c("a"), c("b", "a")], true).nested.size).toBe(0);
  });
});
