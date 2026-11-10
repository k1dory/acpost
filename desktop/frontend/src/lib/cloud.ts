// The cloud's agents as the builder edits them: a definition, what it is checked and deployed as.

export type CloudSpec = { name: string; description: string; policy: string; environment: string; repo: string; instructions: string; connections: string[] };
export type CloudDef = CloudSpec & { ref: string; shared: boolean; revision: number };
export type CloudItem = { ref: string; name: string; shared: boolean; revision: number; description?: string };
export type Catalogue = { cloud?: string; agents: CloudDef[]; environments: CloudItem[]; connections: CloudItem[]; policies: string[] };
export type CloudPlan = { plan: string; valid: boolean; action: string; revision: number; next_revision: number; diff: { path: string; from: string; to: string }[]; errors: string[] };

export const emptySpec = (): CloudSpec => ({ name: "", description: "", policy: "", environment: "", repo: "", instructions: "", connections: [] });

/** A definition as it is sent: what the cloud ignores (spaces around, an empty connection) taken off. */
export function cleanSpec(s: CloudSpec): CloudSpec {
  const conns: string[] = [];
  for (const c of s.connections ?? []) { const v = c.trim(); if (v && !conns.includes(v)) conns.push(v); }
  return { name: s.name.trim(), description: s.description.trim(), policy: s.policy.trim(), environment: s.environment.trim(), repo: s.repo.trim(), instructions: s.instructions.trim(), connections: conns };
}

/** What a check was made of: a check stands only while the definition is the one it checked. */
export const specKey = (s: CloudSpec) => JSON.stringify(cleanSpec(s));

/** What the form cannot send yet (the first thing missing), or "". */
export function specMissing(s: CloudSpec): string {
  const c = cleanSpec(s);
  if (!/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(c.name)) return "A name: lowercase letters, digits and dashes";
  if (!c.policy) return "Choose a policy";
  if (!c.environment) return "Choose an environment";
  return "";
}

/** A dropdown's items with the value the definition has now among them (an environment stored
 * with its revision, a policy the list no longer has): choosing nothing must not change it. */
export function withCurrent(items: { value: string; label: string }[], current: string) {
  return !current || items.some(i => i.value === current) ? items : [{ value: current, label: current }, ...items];
}

/** A drafted definition laid over the form's. What the agent left empty keeps the form's value;
 * what it named that the cloud does not have is dropped (said in `dropped`); and a field the
 * person changed since the draft was asked for (`start`: the form then) stays theirs. */
export function mergeDraft(form: CloudSpec, draft: CloudSpec, cat: Catalogue, isNew: boolean, start: CloudSpec = form): { spec: CloudSpec; dropped: string[] } {
  const d = cleanSpec(draft), f = cleanSpec(form), s = cleanSpec(start), dropped: string[] = [];
  const known = (v: string, list: string[], what: string) => {
    if (!v) return "";
    if (list.includes(v) || list.includes(v.replace(/@\d+$/, ""))) return v;
    dropped.push(`${what} ${v}`);
    return "";
  };
  const envs = cat.environments.map(e => e.ref), conns = cat.connections.map(c => c.ref);
  const text = (k: "description" | "policy" | "environment" | "repo" | "instructions", v: string) => (f[k] !== s[k] ? form[k] : v || form[k]);
  const spec: CloudSpec = {
    name: isNew && f.name === s.name ? d.name || form.name : form.name, // an agent in the catalogue keeps its name
    description: text("description", d.description),
    policy: text("policy", known(d.policy, cat.policies, "policy")),
    environment: text("environment", known(d.environment, envs, "environment")),
    repo: text("repo", d.repo),
    instructions: text("instructions", d.instructions),
    connections: [...form.connections],
  };
  if (d.connections.length && JSON.stringify(f.connections) === JSON.stringify(s.connections)) {
    spec.connections = d.connections.map(c => known(c, conns, "connection")).filter(Boolean);
  }
  return { spec, dropped };
}

/** The agents that can write a draft: this node's ACP agents (an agent of this node carries the
 * node's name too; one reached through the hub is addressed name@node). */
export function localDrafters<T extends { address: string; driver?: string }>(agents: T[]): T[] {
  return agents.filter(a => a.driver === "acp" && !a.address.includes("@"));
}
