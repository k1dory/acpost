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

  /** Each load and connect: an answer of an earlier one is not shown over a later one's. */
  private gen = 0;

  async load() {
    const gen = ++this.gen;
    let info: CloudInfo, cat: Catalogue | null = null, err = "";
    try { info = (await Node.CloudInfo()) as any; } catch (e) { err = errText(e); info = { configured: false }; }
    if (!err && info.configured && !info.error) try { cat = (await Node.CloudBuild()) as any; } catch (e) { err = errText(e); }
    if (gen !== this.gen) return;
    // An editor is of the cloud its catalogue came from: over another's it closes.
    if (this.ed && cat?.cloud !== this.cat?.cloud) this.close();
    this.info = info; this.cat = cat;
    if (err) this.err = err;
  }

  /** Connects a cloud. Whatever was open was another cloud's (or this one's under another key):
   * the editor, its check, a delete clicked once and a draft being written are let go first, and
   * the catalogue with them, until the cloud the node runs with now is read again: also when the
   * connect failed, since the key may be saved and the node start with it later. */
  async connect(url: string, key: string, ca: string): Promise<boolean> {
    if (this.busy) return false;
    this.err = ""; this.note = ""; this.busy = "connect";
    this.close();
    this.gen++; // a load on its way is of the cloud before
    this.info = null; this.cat = null;
    let ok = false, failed = "";
    try {
      const r: any = await Node.CloudConnect(url, key, ca);
      this.note = r.restarted ? t("Connected. The node was started again with the cloud.") : t("The key is saved. Restart the node for it to use the cloud.");
      ok = true;
    } catch (e) { failed = errText(e, errInfo(e).code === "refused" ? "The cloud did not accept the key" : undefined); }
    this.close(); // nothing opened while it connected stays either
    await Promise.all([this.load(), app.loadCloudAgents(), app.loadAgents(), app.loadChats()].map(p => p.catch(() => {})));
    if (failed) this.err = failed;
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
    const cloud = this.cat?.cloud;
    if (!e || !cloud || this.busy) return;
    const k = specKey(e.spec);
    this.err = ""; this.busy = "check";
    try { const p = (await Node.CloudPlanAgent(cloud, cleanSpec(e.spec) as any)) as any; e.plan = p; e.planKey = k; } catch (x) { if (this.ed === e) this.err = errText(x); }
    this.busy = "";
  }

  async deploy() {
    const e = this.ed, cloud = this.cat?.cloud;
    if (!e?.plan || !cloud || !this.checked || this.busy) return;
    const sent = specKey(e.spec);
    this.err = ""; this.busy = "deploy";
    try {
      const r: any = await Node.CloudDeployAgent(cloud, cleanSpec(e.spec) as any, e.plan.plan);
      this.note = t("{ref} is revision {n} in the catalogue. New cloud tasks start from it; sandboxes made earlier keep their revision.", { ref: r.ref, n: r.revision });
      if (this.ed === e) {
        if (specKey(e.spec) === sent && !this.drafting) this.close();
        else {
          // Changed (or being drafted) while it was deployed: that is not in the catalogue, and
          // stays in the form, of an agent that is there now, to be checked and deployed again.
          e.plan = null; e.isNew = false; e.rev = r.revision;
          this.note += " " + t("What you changed meanwhile is still in the form, not deployed.");
        }
      }
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
    const cloud = this.cat?.cloud;
    if (!e || !cloud || e.isNew || this.busy) return;
    const name = e.spec.name;
    if (this.sure !== name) { this.sure = name; return; }
    this.err = ""; this.busy = "delete";
    try {
      await Node.CloudDeleteAgent(cloud, name);
      this.note = t("{name} is out of the catalogue. Its sandboxes stay.", { name });
      if (this.ed === e) this.close(); // another editor opened meanwhile is not this one's to close
      await Promise.all([this.load(), app.loadCloudAgents()]);
    } catch (x) { this.err = errText(x); }
    this.busy = ""; this.sure = "";
  }

  /** The draft's task while it is not known to have ended: what a stop cancels, whether its
   * answer is still waited for or not (a wait that failed leaves the task running). */
  private live = $state("");
  get draftLive() { return !!this.live; }

  /** Has an agent of this Mac write the definition. Its answer fills what the person did not
   * change while it was written; a draft stopped before its task was known is cancelled once it is. */
  async writeDraft() {
    const e = this.ed, cat = this.cat;
    if (!e || !cat?.cloud || this.drafting || !this.ask.trim() || !this.draftAgent) return;
    this.stopDraft(); // one left running by a wait that failed
    const run = ++this.draftRun;
    const start = cleanSpec(e.spec);
    const has = start.name || start.instructions || start.policy || start.environment || start.description;
    this.err = ""; this.draftNote = ""; this.draft = null; this.drafting = true;
    const mine = () => run === this.draftRun && this.ed === e;
    try {
      let res: any = await Node.CloudDraft({ cloud: cat.cloud, agent: this.draftAgent, ask: this.ask.trim(), cwd: this.draftDir, current: has ? start : null } as any);
      if (!mine()) { this.cancelDraft(res.msg); return; }
      this.draft = { msg: res.msg, thread: res.thread };
      this.live = res.msg;
      while (mine() && !res.spec && !res.error) res = await Node.CloudDraftResult(res.msg);
      if (!mine()) return;
      this.live = ""; // it ended
      if (res.error) this.err = tr(res.error);
      else {
        const m = mergeDraft(e.spec, res.spec, cat, e.isNew, start);
        e.spec = m.spec;
        this.draftNote = m.dropped.length ? t("The draft named what the cloud does not have, left out: {list}", { list: m.dropped.join(", ") }) : t("The draft is in the form. Read it, check it, deploy it.");
      }
    } catch (x) { if (mine()) this.err = errText(x); }
    if (run === this.draftRun) this.drafting = false;
  }
  /** Stops the draft: its answer is not waited for, and its task, if it may still run, is cancelled. */
  stopDraft() {
    this.draftRun++; this.drafting = false;
    const msg = this.live;
    this.live = "";
    if (msg) this.cancelDraft(msg);
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

/** The app's builder: one for the app, so what it is doing (a connect on its way) and what it
 * has open do not start anew each time the settings are opened. */
export const cloud = new CloudBuilder();
