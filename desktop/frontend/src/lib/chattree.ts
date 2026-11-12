// Chats an agent started from another chat (what it asked another agent while it worked there:
// a subagent's, a teammate's chat) are listed under that chat.

type Node = { key: string; parent?: string };

/** A chat as the list shows it: how deep under its root it is. */
export type Row<T> = { chat: T; depth: number };

/** The list as branches: each root (a chat with no parent listed) with all that came from it, in
 * rows — the root, then its descendants depth first, siblings in the list's order. Branches are in
 * the order of their first chat in the list, whichever it is: with the list newest first, a branch
 * a subagent is active in is where that subagent's chat would be. A chat whose parent is not
 * listed, or that is its own ancestor, is a root. flat: every chat is (a filter lists what
 * matches). Not recursive: a chain is as long as the list. */
export function chatTree<T extends Node>(chats: T[], flat = false): { branches: Row<T>[][]; nested: Set<string> } {
  const nested = new Set<string>();
  if (flat) return { branches: chats.map(c => [{ chat: c, depth: 0 }]), nested };
  const by = new Map(chats.map(c => [c.key, c]));
  const loops = (c: T) => {
    const seen = new Set([c.key]);
    for (let p = c.parent; p; p = by.get(p)?.parent) {
      if (seen.has(p)) return true;
      seen.add(p);
    }
    return false;
  };
  const kids = new Map<string, T[]>(), rootOf = new Map<string, string>();
  for (const c of chats) {
    if (!c.parent || !by.has(c.parent) || loops(c)) continue;
    nested.add(c.key);
    kids.set(c.parent, [...(kids.get(c.parent) ?? []), c]);
  }
  const root = (c: T) => {
    const path: string[] = [];
    let k = c.key;
    while (nested.has(k) && !rootOf.has(k)) { path.push(k); k = by.get(k)!.parent!; }
    const r = rootOf.get(k) ?? k;
    for (const p of path) rootOf.set(p, r);
    return r;
  };
  const branches: Row<T>[][] = [], done = new Set<string>();
  for (const c of chats) {
    const r = root(c);
    if (done.has(r)) continue;
    done.add(r);
    const rows: Row<T>[] = [], stack: Row<T>[] = [{ chat: by.get(r)!, depth: 0 }];
    while (stack.length) {
      const row = stack.pop()!;
      rows.push(row);
      const ks = kids.get(row.chat.key) ?? [];
      for (let i = ks.length - 1; i >= 0; i--) stack.push({ chat: ks[i], depth: row.depth + 1 });
    }
    branches.push(rows);
  }
  return { branches, nested };
}
