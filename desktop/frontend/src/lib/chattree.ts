// Chats an agent started from another chat (what it asked another agent while it worked there:
// a subagent's, a teammate's chat) are listed under that chat.

type Node = { key: string; parent?: string };

/** kids: each chat's own, in the list's order; nested: the chats listed under another (the rest
 * are listed as before). A chat whose parent is not in the list, or that is its own ancestor, is
 * not nested. flat: nothing is (a filter lists what matches). */
export function chatTree<T extends Node>(chats: T[], flat = false): { kids: Map<string, T[]>; nested: Set<string> } {
  const kids = new Map<string, T[]>(), nested = new Set<string>();
  if (flat) return { kids, nested };
  const by = new Map(chats.map(c => [c.key, c]));
  const loops = (c: T) => {
    const seen = new Set([c.key]);
    for (let p = c.parent; p; p = by.get(p)?.parent) {
      if (seen.has(p)) return true;
      seen.add(p);
    }
    return false;
  };
  for (const c of chats) {
    if (!c.parent || !by.has(c.parent) || loops(c)) continue;
    nested.add(c.key);
    kids.set(c.parent, [...(kids.get(c.parent) ?? []), c]);
  }
  return { kids, nested };
}
