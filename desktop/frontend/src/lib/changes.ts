// The Changes panel's data: what a whole chat changed, from what each of its messages changed.
// The node answers per message (its turns and its subagents': GET /v1/messages/{id}/changes);
// a chat is several messages of the person, each a root of its own (a later one is not a child
// of the first), so the panel asks each and puts the answers together here.

type File = { path: string; status: string; added: number; removed: number };
export type TurnChanges = { msg: string; agent: string; start: string; at: string; repo: string; branch?: string; before: string; after: string; files?: File[]; diff?: string; cut?: boolean };
export type RepoDiff = { repo: string; branch?: string; before: string; after: string; files?: File[]; diff?: string; cut?: boolean; parts?: number };
export type Changes = { turns: TurnChanges[]; repos: RepoDiff[] };

/** The messages a chat's changes are asked of: the roots of its tree, those to an agent whose
 * parent is not among the chat's messages (the node's answer for one takes its children in). A
 * task delegated through the hub arrives with depth 1 and a parent on another node: it is a root
 * here. None (a chat not read yet): the focused message, so the panel is not left empty while
 * the node has changes. */
export function changeRoots(msgs: { id: string; parent?: string; to: string }[], focus = ""): string[] {
  const here = new Set(msgs.map(m => m.id));
  const roots = msgs.filter(m => m.to.startsWith("agent:") && !(m.parent && here.has(m.parent))).map(m => m.id);
  return roots.length ? roots : focus ? [focus] : [];
}

/** The answers for each root, in the chat's order, as one: every turn, and each repository once.
 * A repository one root changed is its diff as the node made it. One several roots changed is
 * their diffs one after another (parts: how many): the node has no diff over a whole thread,
 * so its hunks are those of each message's span, not one diff from the first tree to the last. */
export function mergeChanges(list: (Changes | null | undefined)[]): Changes {
  const turns: TurnChanges[] = [];
  const byRepo = new Map<string, RepoDiff[]>();
  for (const c of list) {
    if (!c) continue;
    turns.push(...(c.turns ?? []));
    for (const r of c.repos ?? []) byRepo.set(r.repo, [...(byRepo.get(r.repo) ?? []), r]);
  }
  turns.sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
  const repos = [...byRepo.values()].map(rs => rs.length === 1 ? rs[0] : joinRepo(rs));
  return { turns, repos };
}

function joinRepo(rs: RepoDiff[]): RepoDiff {
  const files = new Map<string, File>();
  for (const r of rs) for (const f of r.files ?? []) {
    const s = files.get(f.path) ?? { path: f.path, status: f.status, added: 0, removed: 0 };
    s.added += Math.max(0, f.added); s.removed += Math.max(0, f.removed); s.status = f.status;
    files.set(f.path, s);
  }
  const last = rs[rs.length - 1];
  return {
    repo: last.repo, branch: last.branch, before: rs[0].before, after: last.after, files: [...files.values()],
    diff: rs.map(r => r.diff ?? "").filter(Boolean).join("\n"), cut: rs.some(r => r.cut), parts: rs.length,
  };
}

/** Diffs of one file (repository and path) that came apart (several turns, several roots): one
 * entry, its hunks in order. */
export function joinFiles<T extends { repo: string; path: string; lines: any[] }>(files: T[]): T[] {
  const out: T[] = [];
  const at = new Map<string, T>();
  for (const f of files) {
    const k = f.repo + "\u0000" + f.path;
    const have = at.get(k);
    if (have) have.lines = [...have.lines, ...f.lines];
    else { const c = { ...f, lines: [...f.lines] }; at.set(k, c); out.push(c); }
  }
  return out;
}
