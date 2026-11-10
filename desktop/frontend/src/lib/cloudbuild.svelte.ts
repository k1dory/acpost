// The builder of the cloud's agents: the cloud the node is connected to, its catalogue, the
// definition being written, its check and deploy, and a draft written by an agent of this Mac.
// What is on the screen belongs to one cloud: connecting another closes it all.
import { t, tr } from "./i18n.svelte";
import { app } from "./state.svelte";
import { Node, errText, errInfo } from "./api";
import { cleanSpec, emptySpec, mergeDraft, specKey, specMissing, type Catalogue, type CloudDef, type CloudPlan, type CloudSpec } from "./cloud";

export type CloudInfo = { configured: boolean; url?: string; tenant?: string; admin?: boolean; subject?: string; expires?: string; error?: string; error_code?: string };
export type Edit = { isNew: boolean; rev: number; spec: CloudSpec; plan: CloudPlan | null; planKey: string };

const specOf = (a: CloudSpec): CloudSpec => ({ name: a.name, description: a.description ?? "", policy: a.policy ?? "", environment: a.environment ?? "", repo: a.repo ?? "", instructions: a.instructions ?? "", connections: [...(a.connections ?? [])] });

export class CloudBuilder {
  info = $state<CloudInfo | null>(null);
  cat = $state<Catalogue | null>(null);
  err = $state("");
  note = $state("");
  busy = $state<"" | "connect" | "check" | "deploy" | "delete">("");

  /** The agent being written (null: the list). */
  ed = $state<Edit | null>(null);
  /** The agent whose delete was clicked once. */
  sure = $state("");

  ask = $state("");
  draftAgent = $state("");
  draftDir = $state("");
  drafting = $state(false);
  draft = $state<{ msg: string; thread: string } | null>(null);
  draftNote = $state("");
  private draftRun = 0;

  /** A check stands only for the definition it checked. */
  get checked() { return !!this.ed?.plan && this.ed.planKey === specKey(this.ed.spec); }
  get missing() { return this.ed ? specMissing(this.ed.spec) : ""; }

  async load() {
    try { this.info = (await Node.CloudInfo()) as any; } catch (e) { this.err = errText(e); this.info = { configured: false }; this.cat = null; return; }
    if (!this.info?.configured || this.info.error) { this.cat = null; return; }
    try { this.cat = (await Node.CloudBuild()) as any; } catch (e) { this.err = errText(e); this.cat = null; }
  }

  /** Connects a cloud. Whatever was open was another cloud's (or this one's under another key):
   * the editor, its check, a delete clicked once and a draft being written are let go first. */
  async connect(url: string, key: string, ca: string): Promise<boolean> {
    if (this.busy) return false;
    this.err = ""; this.note = ""; this.busy = "connect";
    this.close();
    let ok = false;
    try {
      const r: any = await Node.CloudConnect(url, key, ca);
      this.close(); // nothing opened while it connected stays either
      this.info = null; this.cat = null;
      this.note = r.restarted ? t("Connected. The node was started again with the cloud.") : t("The key is saved. Restart the node for it to use the cloud.");
      await Promise.all([this.load(), app.loadCloudAgents(), app.loadAgents(), app.loadChats()]);
      ok = true;
    } catch (e) { this.err = errText(e, errInfo(e).code === "refused" ? "The cloud did not accept the key" : undefined); }
    this.busy = "";
    return ok;
  }

  private open(e: Edit) {
    this.stopDraft();
    this.ed = e; this.err = ""; this.note = ""; this.ask = ""; this.draftNote = ""; this.draft = null; this.sure = "";
  }
  /** Closes the editor: its draft is stopped, its delete forgotten. */
  close() { this.stopDraft(); this.ed = null; this.sure = ""; this.draft = null; this.draftNote = ""; }

  newAgent(from?: CloudDef) {
    if (this.busy) return;
    const spec = from ? specOf(from) : emptySpec();
    if (!from && this.cat?.policies.length === 1) spec.policy = this.cat.policies[0];
    if (!from && this.cat?.environments.length === 1) spec.environment = this.cat.environments[0].ref;
    this.open({ isNew: true, rev: 0, spec, plan: null, planKey: "" });
  }
  editAgent(a: CloudDef) {
    if (this.busy) return;
    this.open({ isNew: false, rev: a.revision, spec: specOf(a), plan: null, planKey: "" });
  }
  toggleConn(ref: string) {
    if (!this.ed) return;
    const c = this.ed.spec.connections;
    this.ed.spec.connections = c.includes(ref) ? c.filter(x => x !== ref) : [...c, ref];
  }

  async check() {
    const e = this.ed;
    if (!e || this.busy) return;
    const k = specKey(e.spec);
    this.err = ""; this.busy = "check";
    try { const p = (await Node.CloudPlanAgent(cleanSpec(e.spec) as any)) as any; e.plan = p; e.planKey = k; } catch (x) { if (this.ed === e) this.err = errText(x); }
    this.busy = "";
  }

  async deploy() {
    const e = this.ed;
    if (!e?.plan || !this.checked || this.busy) return;
    this.err = ""; this.busy = "deploy";
    try {
      const r: any = await Node.CloudDeployAgent(cleanSpec(e.spec) as any, e.plan.plan);
      this.note = t("{ref} is revision {n} in the catalogue. New cloud tasks start from it; sandboxes made earlier keep their revision.", { ref: r.ref, n: r.revision });
      if (this.ed === e) this.close();
      await Promise.all([this.load(), app.loadCloudAgents()]);
    } catch (x) {
      this.err = errText(x);
      if (errInfo(x).code === "conflict") e.plan = null; // the catalogue moved on: checked again
    }
    this.busy = "";
  }

  /** Deletes the agent in the editor: the second click of two. */
  async remove() {
    const e = this.ed;
    if (!e || e.isNew || this.busy) return;
    const name = e.spec.name;
    if (this.sure !== name) { this.sure = name; return; }
    this.err = ""; this.busy = "delete";
    try {
      await Node.CloudDeleteAgent(name);
      this.note = t("{name} is out of the catalogue. Its sandboxes stay.", { name });
      if (this.ed === e) this.close(); // another editor opened meanwhile is not this one's to close
      await Promise.all([this.load(), app.loadCloudAgents()]);
    } catch (x) { this.err = errText(x); }
    this.busy = ""; this.sure = "";
  }

  /** Has an agent of this Mac write the definition. Its answer fills what the person did not
   * change while it was written; a draft stopped before its task was known is cancelled once it is. */
  async writeDraft() {
    const e = this.ed, cat = this.cat;
    if (!e || !cat || this.drafting || !this.ask.trim() || !this.draftAgent) return;
    const run = ++this.draftRun;
    const start = cleanSpec(e.spec);
    const has = start.name || start.instructions || start.policy || start.environment || start.description;
    this.err = ""; this.draftNote = ""; this.draft = null; this.drafting = true;
    const mine = () => run === this.draftRun && this.ed === e;
    try {
      let res: any = await Node.CloudDraft({ agent: this.draftAgent, ask: this.ask.trim(), cwd: this.draftDir, current: has ? start : null } as any);
      if (!mine()) { this.cancelDraft(res.msg); return; }
      this.draft = { msg: res.msg, thread: res.thread };
      while (mine() && !res.spec && !res.error) res = await Node.CloudDraftResult(res.msg);
      if (!mine()) return;
      if (res.error) this.err = tr(res.error);
      else {
        const m = mergeDraft(e.spec, res.spec, cat, e.isNew, start);
        e.spec = m.spec;
        this.draftNote = m.dropped.length ? t("The draft named what the cloud does not have, left out: {list}", { list: m.dropped.join(", ") }) : t("The draft is in the form. Read it, check it, deploy it.");
      }
    } catch (x) { if (mine()) this.err = errText(x); }
    if (run === this.draftRun) this.drafting = false;
  }
  stopDraft() {
    const was = this.drafting;
    this.draftRun++; this.drafting = false;
    if (was && this.draft) this.cancelDraft(this.draft.msg);
  }
  private cancelDraft(msg: string) {
    Node.Cancel(msg).catch((x: unknown) => { this.err = errText(x, "The draft's task was not stopped"); });
  }
  async openDraft() {
    const d = this.draft;
    if (!d) return;
    await app.loadChats();
    const c = app.chats.find(c => c.thread === d.thread);
    if (c) app.open(c);
  }
}
