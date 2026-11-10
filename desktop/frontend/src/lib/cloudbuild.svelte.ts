// The builder of the cloud's agents: the cloud the node is connected to, its catalogue, the
// definition being written, its check and deploy, and a draft written by an agent of this Mac.
// What is on the screen belongs to one cloud: connecting another closes it all.
import { t, tr } from "./i18n.svelte";
import { app } from "./state.svelte";
import { Node, errText, errInfo } from "./api";
import { newId } from "./ids";
import { cleanSpec, emptySpec, mergeDraft, specKey, specMissing, type Catalogue, type CloudDef, type CloudPlan, type CloudSpec } from "./cloud";

export type CloudInfo = { configured: boolean; id?: string; url?: string; tenant?: string; admin?: boolean; subject?: string; expires?: string; error?: string; error_code?: string };
/** cloud: the connection whose catalogue the editor was opened from; what it writes goes there only. */
export type Edit = { cloud: string; isNew: boolean; rev: number; spec: CloudSpec; plan: CloudPlan | null; planKey: string };

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
  /** What the last read failed with: gone from the screen once a read succeeds. */
  private loadErr = "";

  async load() {
    const gen = ++this.gen;
    let info: CloudInfo = { configured: false }, cat: Catalogue | null = null, err = "", read = false;
    // The identity and the catalogue of one connection: a node that changed its cloud between
    // the two reads is read again, not shown as one tenant's name over another's agents.
    for (let i = 0; i < 3 && !read && !err; i++) {
      cat = null;
      try { info = (await Node.CloudInfo()) as any; } catch (e) { err = errText(e); break; }
      if (!info.configured || info.error) { read = true; break; }
      try { cat = (await Node.CloudBuild()) as any; } catch (e) { err = errText(e); break; }
      read = !!info.id && cat?.cloud === info.id;
    }
    if (gen !== this.gen) return;
    if (!read && !err) { err = t("The cloud changed while it was read: refresh"); cat = null; }
    if (!err && this.err === this.loadErr) this.err = ""; // what a read said, read now
    this.loadErr = err;
    if (err) {
      // Not read (the node or the cloud did not answer): what is on the screen stays, the form
      // too. Its writes name its connection, and the node refuses them if that is another now.
      this.err = err;
      if (!this.info) this.info = info;
      return;
    }
    const id = info.configured ? info.id ?? "" : "";
    // A confirmed change of the connection: an editor of the one before closes.
    if (this.ed && this.ed.cloud !== id) this.close();
    this.info = info;
    // A cloud that is connected and not answering keeps the catalogue read from it before.
    this.cat = cat ?? (info.configured && this.cat?.cloud === id ? this.cat : null);
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
    const cloud = this.cat?.cloud;
    if (this.busy || !cloud) return;
    const spec = from ? specOf(from) : emptySpec();
    if (!from && this.cat?.policies.length === 1) spec.policy = this.cat.policies[0];
    if (!from && this.cat?.environments.length === 1) spec.environment = this.cat.environments[0].ref;
    this.open({ cloud, isNew: true, rev: 0, spec, plan: null, planKey: "" });
  }
  editAgent(a: CloudDef) {
    const cloud = this.cat?.cloud;
    if (this.busy || !cloud) return;
    this.open({ cloud, isNew: false, rev: a.revision, spec: specOf(a), plan: null, planKey: "" });
  }
  toggleConn(ref: string) {
    if (!this.ed) return;
    const c = this.ed.spec.connections;
    this.ed.spec.connections = c.includes(ref) ? c.filter(x => x !== ref) : [...c, ref];
  }

  async check() {
    const e = this.ed;
    if (!e || this.busy) return;
    const cloud = e.cloud, k = specKey(e.spec);
    this.err = ""; this.busy = "check";
    try { const p = (await Node.CloudPlanAgent(cloud, cleanSpec(e.spec) as any)) as any; e.plan = p; e.planKey = k; } catch (x) { if (this.ed === e) this.err = errText(x); }
    this.busy = "";
  }

  async deploy() {
    const e = this.ed;
    if (!e?.plan || !this.checked || this.busy) return;
    const cloud = e.cloud, sent = specKey(e.spec), name = cleanSpec(e.spec).name;
    this.err = ""; this.busy = "deploy";
    try {
      const r: any = await Node.CloudDeployAgent(cloud, cleanSpec(e.spec) as any, e.plan.plan);
      this.note = t("{ref} is revision {n} in the catalogue. New cloud tasks start from it; sandboxes made earlier keep their revision.", { ref: r.ref, n: r.revision });
      if (this.ed === e) {
        if (specKey(e.spec) === sent && !this.drafting) this.close();
        else {
          // Changed (or being drafted) while it was deployed: that is not in the catalogue, and
          // stays in the form, of an agent that is there now, to be checked and deployed again.
          e.plan = null;
          // Still that agent only under the name it was deployed by: renamed meanwhile, the form
          // is a new agent's (and never the editor, with its delete, of another one of that name).
          if (cleanSpec(e.spec).name === name) { e.isNew = false; e.rev = r.revision; }
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
    if (!e || e.isNew || this.busy) return;
    const cloud = e.cloud, name = e.spec.name;
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

  /** The drafts' tasks not known to have ended: what a stop cancels, whether an answer is still
   * waited for or not (a wait that failed leaves its task running, and so does a cancel that failed:
   * it stays here, and is cancelled again by the next stop). */
  private live = $state<string[]>([]);
  /** A draft asked for whose answer never came: there may be a task nobody here knows the id
   * of. Asked again under the same key it is that task, not another: to wait for, or to stop. */
  private lost = $state<{ key: string; req: any } | null>(null);
  get draftLive() { return this.live.length > 0 || !!this.lost; }
  private ended(msg: string) { this.live = this.live.filter(m => m !== msg); }

  /** Has an agent of this Mac write the definition. Its answer fills what the person did not
   * change while it was written; a draft stopped before its task was known is cancelled once it is. */
  async writeDraft() {
    const e = this.ed, cat = this.cat;
    if (!e || !cat || this.drafting || !this.ask.trim() || !this.draftAgent) return;
    const req = { cloud: e.cloud, agent: this.draftAgent, ask: this.ask.trim(), cwd: this.draftDir };
    // The same request again after its answer was lost: the same key, so the same task.
    const was = this.lost;
    const same = !!was && JSON.stringify(was.req) === JSON.stringify(req);
    const key = same ? was!.key : newId();
    if (same) this.lost = null;
    this.stopDraft(); // one left running by a wait that failed, or asked for and never answered
    const run = ++this.draftRun;
    const start = cleanSpec(e.spec);
    const has = start.name || start.instructions || start.policy || start.environment || start.description;
    this.err = ""; this.draftNote = ""; this.draft = null; this.drafting = true;
    const mine = () => run === this.draftRun && this.ed === e;
    try {
      let res: any;
      try { res = await Node.CloudDraft({ ...req, key, current: has ? start : null } as any); } catch (x) {
        // Refused by the node, there is no task; anything else may have made one.
        if (!["invalid", "refused", "unsupported", "conflict", "not_found"].includes(errInfo(x).code)) this.lost = { key, req };
        throw x;
      }
      const msg: string = res.msg;
      this.live = [...this.live, msg];
      if (!mine()) { this.cancelDraft(msg); return; }
      this.draft = { msg, thread: res.thread };
      while (mine() && !res.spec && !res.error) res = await Node.CloudDraftResult(msg);
      if (res.spec || res.error) this.ended(msg); // whoever waits for it now
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
  /** Stops the draft: its answer is not waited for, and every task that may still run is cancelled. */
  stopDraft() {
    this.draftRun++; this.drafting = false;
    for (const msg of this.live) this.cancelDraft(msg);
    const lost = this.lost;
    if (lost) {
      // Found by its key (made now, if it never was), then stopped like any other.
      Node.CloudDraft({ ...lost.req, key: lost.key, current: null } as any).then((r: any) => {
        if (this.lost === lost) this.lost = null;
        this.live = [...this.live, r.msg];
        this.cancelDraft(r.msg);
      }, (x: unknown) => {
        if (["invalid", "refused", "unsupported", "conflict", "not_found"].includes(errInfo(x).code)) { if (this.lost === lost) this.lost = null; }
        else this.err = errText(x, "The draft's task was not stopped");
      });
    }
  }
  private cancelDraft(msg: string) {
    Node.Cancel(msg).then(() => this.ended(msg), (x: unknown) => {
      // No such message, or one that ended already: nothing left to stop.
      if (["not_found", "conflict"].includes(errInfo(x).code)) this.ended(msg);
      else this.err = errText(x, "The draft's task was not stopped");
    });
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
