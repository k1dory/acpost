import { describe, it, expect } from "vitest";
import { chatTree } from "./chattree";

describe("chats an agent started are listed under the chat they came from", () => {
  const c = (key: string, parent?: string) => ({ key, parent });
  const shape = (chats: { key: string; parent?: string }[], flat = false) =>
    chatTree(chats, flat).branches.map(b => b.map(r => r.chat.key + ":" + r.depth).join(" "));
  it("each branch is its root and what came from it, depth first, in the list's order", () => {
    expect(shape([c("a"), c("b", "a"), c("x"), c("d", "b"), c("e", "a")])).toEqual(["a:0 b:1 d:2 e:1", "x:0"]);
  });
  it("a branch is where its first chat in the list is: a subagent active now brings its root up", () => {
    expect(shape([c("kid", "old"), c("x"), c("y"), c("old")])).toEqual(["old:0 kid:1", "x:0", "y:0"]);
  });
  it("a chat whose parent is not listed, or in a loop, is a root; a filter lists flat", () => {
    expect(shape([c("b", "gone")])).toEqual(["b:0"]);
    expect(shape([c("a", "b"), c("b", "a")])).toEqual(["a:0", "b:0"]);
    expect(shape([c("a", "a")])).toEqual(["a:0"]);
    expect(shape([c("a"), c("b", "a")], true)).toEqual(["a:0", "b:0"]);
  });
  it("a chain as long as the list is listed whole, each chat once", () => {
    const chain = Array.from({ length: 5000 }, (_, i) => c("k" + i, i ? "k" + (i - 1) : undefined));
    const { branches } = chatTree(chain);
    expect(branches).toHaveLength(1);
    expect(branches[0]).toHaveLength(5000);
    expect(branches[0].at(-1)).toEqual({ chat: chain[4999], depth: 4999 });
  });
});
