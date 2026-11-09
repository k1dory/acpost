import { newId } from "./ids";

// What a form creates on the node gets its id with the draft, not from the node's answer: the
// write done and its answer lost, saving again is the same record, not a second one (a second
// schedule runs its task twice).

/** A new schedule's draft (isNew: not saved yet, as far as the form knows). */
export function newScheduleDraft(agent: string, cwd: string) {
  return { id: "sch_" + newId(), isNew: true, name: "", cron: "0 9 * * 1-5", agent, body: "", cwd, enabled: true };
}

/** A new backlog item's draft. */
export function newBacklogDraft(agent: string, cwd: string) {
  return { id: "bl_" + newId(), title: "", body: "", agent, cwd };
}
