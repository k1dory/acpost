import { t, tr } from "./i18n.svelte";
import { bytes } from "./names";
import { Node, on, errText, errInfo } from "./api";
import { newId } from "./ids";
import { ComposerDraft, sendFiles, giveBack, mergeFiles, type Attachment } from "./composer.svelte";
import { afterSent, overLimits } from "./draft";
import { Turn, ownBody, type Ev } from "./turn";

export type Msg = {
  id: string; from: string; to: string; thread: string; parent?: string; depth: number; state: string;
  body?: string; reply?: string; error?: string; error_code?: string; hint?: string; meta?: Record<string, string>;
  created_at: string; updated_at: string; finished_at?: string; files?: { name: string; type: string; size: number; reply?: boolean }[];
};
export type Agent = { name: string; address: string; driver: string; node?: string; queued: number; running: number; process?: boolean };
export type Chat = {
  key: string; kind: "thread" | "session" | "cloud"; agent?: string; title: string; cwd?: string; repo?: string;
  thread?: string; session?: string; session_agent?: string; sandbox?: string; status?: string; updated: string;
  messages?: number; running?: number; queued?: number; failed?: number; folder?: string; cloud_agent?: string;
};
export type Folder = { id: string; name: string; pos: number; chats: string[] };
export type Option = { id: string; name: string; category?: string; current: string; values: { value: string; name: string; description?: string; group?: string }[] };
export type Options = { agent: string; options: Option[]; commands?: { name: string; description?: string }[] };
export type CloudAgent = { ref: string; name: string; description?: string; revision: number };
/** A conversation started elsewhere, read back: what the user said, and the turn that answered. */
export type Segment = { user: string; turn: Turn };

export const final = (s: string) => s === "done" || s === "failed" || s === "cancelled" || s === "expired";

/** Where a new task runs, which agent, in which directory. */
export type Draft = { where: "mac" | "cloud"; agent: string; cloudAgent: string; cwd: string; repo: string };
/** What the composer sends with, chosen for one agent ("" is the agent's own default). */
export type Settings = { model: string; effort: string; mode: string };
/** Which of an agent's options each setting is. */
const settingCats: Record<keyof Settings, string> = { model: "model", effort: "thought_level", mode: "mode" };
/** The node's limits on what a message carries (GET /v1/health). */
export type Limits = { max_body: number; max_files: number; max_files_size: number };

/** A page of a message's events, as the node gives them at most (store.MaxPage): a full page means
 * there may be more. */
export const eventsPage = 500;
/** Codes of a stream that will not come back by asking again. */
const lasting = new Set(["not_found", "invalid", "refused", "unsupported"]);
/** How many times a broken stream is asked again (1 s, 2 s, … 30 s apart) before it is given up. */
const followTries = 6;

export type PreviewEvent = { kind: string; message?: string; source?: string; line?: number; col?: number; stack?: string; url?: string; title?: string; main_frame?: boolean };
export type PanelTab = "activity" | "changes" | "terminal" | "prs" | "preview" | "artifacts";
function savedPanel(): boolean { try { return localStorage.getItem("panel-open") !== "0"; } catch { return true; } }
const panelTabs: PanelTab[] = ["activity", "changes", "terminal", "prs", "preview", "artifacts"];
/** An artifact to show: a reply's attachment, or a file of this Mac a reply links to. */
export type ArtifactRef = { msg?: string; name: string; path?: string; base?: string };
function savedTab(): PanelTab {
  try { const v = localStorage.getItem("panel-tab") as PanelTab; if (savedTabs().includes(v)) return v; } catch {}
  return savedTabs()[0] ?? "activity";
}
function savedTabs(): PanelTab[] {
  try {
    const v = JSON.parse(localStorage.getItem("panel-tabs") ?? "null");
    if (Array.isArray(v)) return [...new Set(v.filter((x): x is PanelTab => panelTabs.includes(x)))];
  } catch {}
  return ["activity"];
}

class App {
  conn = $state<{ ok: boolean; error?: string; url?: string; node?: string; you?: string } | null>(null);
  agents = $state<Agent[]>([]);
  chats = $state<Chat[]>([]);
  folders = $state<Folder[]>([]);
  partial = $state<string[]>([]);
  loadingChats = $state(false);
  chat = $state<Chat | null>(null);      // the open chat (null: a new task)
  thread = $state<string>("");           // its acpost thread, once there is one
  msgs = $state<Msg[]>([]);
  history = $state<Segment[]>([]);       // a session's or sandbox's conversation before acpost
  historyLoading = $state(false);
  turns = $state<Record<string, Turn>>({});
  rev = $state(0);
  focus = $state<string>("");
  tab = $state<PanelTab>(savedTab());
  /** The right panel's open tabs, in order (the + adds one, × closes it); kept between launches. */
  tabs = $state<PanelTab[]>(savedTabs());
  openTab(k: PanelTab) {
    if (!this.tabs.includes(k)) { this.tabs = [...this.tabs, k]; this.saveTabs(); }
    this.showTab(k);
    this.setPanel(true);
  }
  closeTab(k: PanelTab) {
    const i = this.tabs.indexOf(k);
    if (i < 0) return;
    this.tabs = this.tabs.filter(x => x !== k);
    if (this.tab === k) this.showTab(this.tabs[Math.min(i, this.tabs.length - 1)] ?? "activity");
    this.saveTabs();
  }
  private saveTabs() { try { localStorage.setItem("panel-tabs", JSON.stringify(this.tabs)); } catch {} }
  /** Shows an open tab (the one shown is kept between launches too). */
  showTab(k: PanelTab) {
    this.tab = k;
    try { localStorage.setItem("panel-tab", k); } catch {}
  }
  /** The right panel shown (kept between launches: hidden stays hidden). */
  panel = $state(savedPanel());
  setPanel(on: boolean) {
    this.panel = on;
    try { localStorage.setItem("panel-open", on ? "1" : "0"); } catch {}
  }
  view = $state<"chat" | "usage" | "settings" | "board" | "wiki" | "ide">("chat");
  playbooks = $state<{ name: string; body: string }[]>([]);
  quote = $state("");     // text to put in the composer (⌘L)
  palette = $state(false); // ⌘K
  viewMenu = $state(false); // the title bar's view menu open
  asking = $state<any[]>([]); // approvals waiting
  theme = $state<"light" | "dark" | "system">("light");
  target = $state<string>("");
  draft = $state<Draft>({ where: "mac", agent: "agent:claude", cloudAgent: "", cwd: "", repo: "" });
  /** Each agent's settings (a Claude model is not a Codex one). */
  settings = $state<Record<string, Settings>>({});
  limits = $state<Limits | null>(null);
  /** Messages whose stream stopped for good (or whose events could not be read): why. */
  broken = $state<Record<string, string>>({});
  options = $state<Record<string, Options>>({});
  cloudAgents = $state<CloudAgent[]>([]);
  home = $state("");
  error = $state("");
  /** The messages followed: the listener's off, and the subscription's generation (Follow and
   * Unfollow carry it; the app lets only a message's newest generation change its stream, so calls
   * that Wails runs out of order end as asked last). */
  private unsub = new Map<string, { off: () => void; sub: number }>();
  /** Bumped each time the screen changes chat: an answer to a call made before is not this chat's. */
  private gen = 0;

  /** When the app first saw each message running (its own times say when it was sent or changed). */
  runningSince = new Map<string, number>();
  seenRunning(id: string): number {
    let t = this.runningSince.get(id);
    if (t === undefined) { t = Date.now(); this.runningSince.set(id, t); }
    return t;
  }

  /** Where this node's chat list is kept between launches: by node, address and who. */
  chatsKey() {
    return "chats:" + [this.conn?.node, this.conn?.url, this.conn?.you].map(v => v ?? "").join("|");
  }

  async connect() {
    let c: any;
    try { c = await Node.Status(); } catch (e) { c = { ok: false, error: errText(e) }; } // the app's side failed: shown as the node down
    const back = this.conn !== null && !this.conn.ok && c.ok;
    this.conn = { ok: c.ok, error: c.error, url: c.url, node: c.status?.node, you: c.status?.you };
    if (!c.ok) return;
    if (c.status?.limits) this.limits = c.status.limits;
    try { await this.connected(); } catch (e) { this.error = errText(e); }
    // The node back after it was lost: the open chat's turns still running are followed again.
    if (back) for (const m of this.msgs) if (!final(m.state)) this.watch(m);
  }

  private async connected() {
    // This node's list as it was last time, at once; its answer replaces it.
    try {
      const saved = JSON.parse(localStorage.getItem(this.chatsKey()) ?? "null");
      if (saved?.items && !this.chats.length) { this.chats = saved.items; this.folders = saved.folders ?? []; }
    } catch {}
    this.home = await Node.Home();
    if (!this.draft.cwd) this.draft.cwd = this.home;
    await Promise.all([this.loadAgents(), this.loadChats(), this.loadCloudAgents(), this.loadPlaybooks(), this.watchAsking()]);
    const local = this.localAgents();
    if (!local.some(a => a.address === this.draft.agent) && local[0]) this.draft.agent = local[0].address;
    this.loadOptions(this.draft.agent);
  }

  localAgents() { return this.agents.filter(a => a.driver === "acp"); }

  async loadAgents() {
    try { this.agents = (await Node.Agents()) as any; } catch (e) { this.failed(e); }
  }

  /** A call failed: said, and a node that stopped answering is shown as down (the app's polling
   * then connects again, and follows what still runs). */
  private failed(e: unknown) {
    const code = errInfo(e).code;
    if ((code === "node_down" || code === "unavailable") && this.conn?.ok) this.conn = { ...this.conn, ok: false, error: errText(e) };
    else this.error = errText(e);
  }

  /** The cloud: "" none configured, "ok", or "down" (configured, not answering: cloudError says why;
   * the catalogue last seen stays, so it can be chosen once it is back). */
  cloudState = $state<"" | "ok" | "down">("");
  cloudError = $state("");
  async loadCloudAgents() {
    try {
      const cat: any = await Node.CloudAgents();
      if (!cat?.configured) { this.cloudState = ""; this.cloudAgents = []; if (this.draft.where === "cloud") this.draft.where = "mac"; return; }
      this.cloudAgents = cat.items ?? [];
      this.cloudState = "ok"; this.cloudError = "";
      try { localStorage.setItem("cloud-agents", JSON.stringify(this.cloudAgents)); } catch {}
      if (!this.draft.cloudAgent && this.cloudAgents[0]) this.draft.cloudAgent = this.cloudAgents[0].ref;
    } catch (e) {
      // A node with no cloud answers without an error: an error is the configured cloud failing.
      this.cloudState = "down";
      this.cloudError = tr(errInfo(e).message); // the reason only: "down" is said by the app
      if (!this.cloudAgents.length) try { this.cloudAgents = JSON.parse(localStorage.getItem("cloud-agents") ?? "[]"); } catch {}
    }
  }

  private wasRunning = new Map<string, number>();
  private asked = new Set<string>(); // approvals notified, of those asking now

  /** A notification of the system, when the app is not in front (and the user wants them). */
  notify(title: string, body: string) {
    let on = true;
    try { on = localStorage.getItem("notify") !== "0"; } catch {}
    if (on && !document.hasFocus()) Node.Notify(title, body).catch(() => {});
  }

  /** What waits for the user: approvals asked (notified once each). */
  async watchAsking() {
    try {
      const reqs = ((await Node.Asking()) ?? []) as any[];
      this.asking = reqs;
      // Those still asking are remembered (one answered is no longer: the set does not grow).
      const now = new Set<string>();
      for (const r of reqs) {
        const id = r.request_id ?? r.id;
        if (!id) continue;
        now.add(id);
        if (!this.asked.has(id)) this.notify(t("Needs approval"), r.title || t("An agent asks"));
      }
      this.asked = now;
    } catch {}
  }

  private chatsAsked = 0;
  async loadChats() {
    // Lists asked at once answer in any order: only the last asked is kept.
    const my = ++this.chatsAsked;
    this.loadingChats = true;
    try {
      const c: any = await Node.Chats();
      if (my !== this.chatsAsked) return;
      // A thread that ran and does not now: its turn ended. (Only the chats listed now are kept.)
      const running = new Map<string, number>();
      for (const ch of (c.items ?? []) as Chat[]) {
        if (ch.kind !== "thread") continue;
        const was = this.wasRunning.get(ch.key) ?? 0;
        if (was > 0 && !ch.running && !ch.queued) this.notify(ch.failed ? t("Failed") : t("Done"), ch.title);
        running.set(ch.key, (ch.running ?? 0) + (ch.queued ?? 0));
      }
      this.wasRunning = running;
      this.chats = c.items ?? [];
      this.linkDrafts(this.chats);
      try { localStorage.setItem(this.chatsKey(), JSON.stringify({ items: this.chats, folders: c.folders ?? [] })); } catch {}
      // A new task's thread, once listed, is the open chat (its session, to fork, comes with it).
      if (this.thread && (!this.chat || this.chat.key === "thread:" + this.thread)) {
        const mine = this.chats.find(x => x.key === "thread:" + this.thread);
        if (mine) this.chat = mine;
      }
      this.folders = c.folders ?? [];
      this.partial = c.partial ?? [];
    } catch (e) { if (my === this.chatsAsked) this.failed(e); }
    if (my === this.chatsAsked) this.loadingChats = false;
  }

  // An agent's choices (model, effort, mode, commands): the ones seen last time at once (asking a
  // fresh agent process takes seconds), then the agent's answer.
  private optionsAsked = new Set<string>();
  async loadOptions(agent: string) {
    if (!agent || this.optionsAsked.has(agent) || !this.localAgents().some(a => a.address === agent)) return;
    this.optionsAsked.add(agent);
    if (!this.options[agent]) try {
      const kept = JSON.parse(localStorage.getItem("options:" + agent) ?? "null");
      if (kept) this.options = { ...this.options, [agent]: kept };
    } catch {}
    try {
      const got = (await Node.Options(agent)) as any;
      this.options = { ...this.options, [agent]: got };
      try { localStorage.setItem("options:" + agent, JSON.stringify(got)); } catch {}
    } catch { this.optionsAsked.delete(agent); /* the agent may not start: asked again next time */ }
  }

  /** Recent directories of the user's chats, newest first. */
  recentDirs(): string[] {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const c of this.chats) if (c.cwd && !seen.has(c.cwd)) { seen.add(c.cwd); out.push(c.cwd); }
    return out.slice(0, 12);
  }

  private reset() {
    this.gen++;
    this.historyLoading = false;
    this.broken = {};
    this.artifact = null; // artifacts are the chat's
    this.openedArtifacts = [];
    for (const [id, { off, sub }] of this.unsub) { off(); Node.Unfollow(id, sub); }
    this.unsub.clear();
    this.msgs = [];
    this.history = [];
    this.turns = {};
    this.focus = "";
    this.thread = "";
    if (this.view !== "ide") this.view = "chat"; // in the IDE, another chat opens beside it
  }

  /** A new task. */
  newTask() {
    this.reset();
    // A draft of its own: a send still going owns the last one (its answer edits that, not this).
    // What was typed carries over, unless it is what is being sent.
    const old = this.draftMap.get("new"), d = new ComposerDraft("new");
    if (old && !old.busy) { d.text = old.text; d.files = old.files; old.forward(d); }
    this.draftMap.set("new", d);
    this.drafts++;
    this.chat = null;
  }

  async open(chat: Chat) {
    this.linkDrafts([chat]);
    this.reset();
    this.chat = chat;
    this.target = chat.agent ?? "";
    if (chat.kind === "thread" && chat.thread) {
      await this.openThread(chat.thread);
      return;
    }
    this.historyLoading = true;
    try {
      const evs = (chat.kind === "cloud"
        ? await Node.CloudHistory(chat.sandbox!)
        : await Node.SessionHistory(chat.agent!, chat.session!, chat.cwd ?? "")) as any as Ev[];
      if (this.chat?.key !== chat.key) return;
      this.history = segments(evs ?? []);
    } catch (e) { if (this.chat?.key !== chat.key) return; this.error = errText(e); }
    this.historyLoading = false;
    this.loadOptions(this.target);
  }

  private async openThread(thread: string) {
    this.thread = thread;
    try {
      const ms = ((await Node.Thread(thread)) ?? []) as any as Msg[];
      if (this.thread !== thread) return;
      // What the screen got meanwhile (a message sent, a state streamed) is newer than a list
      // asked before it: merged, never dropped.
      this.msgs = mergeMsgs(ms, this.msgs);
      for (const m of this.msgs) this.watch(m);
      this.focus = ms[ms.length - 1]?.id ?? "";
      const to = ms.find(m => m.to.startsWith("agent:"))?.to;
      if (to) this.target = to;
      this.loadOptions(this.target);
    } catch (e) { this.error = errText(e); }
  }

  watch(m: Msg) {
    if (!m.to.startsWith("agent:")) return;
    const t = (this.turns[m.id] ??= new Turn());
    if (final(m.state)) this.readEvents(m.id, t);
    else this.follow(m.id, t, 0);
  }

  /** A finished message's events, page after page (the node gives a page at a time). */
  private async readEvents(id: string, t: Turn) {
    const gen = this.gen;
    try {
      for (;;) {
        const from = t.last;
        const evs = ((await Node.Events(id, from)) ?? []) as any as Ev[];
        if (gen !== this.gen) return;
        t.add(evs);
        this.rev++;
        if (evs.length < eventsPage || t.last <= from) return;
      }
    } catch (e) { if (gen === this.gen) this.broken = { ...this.broken, [id]: errText(e) }; }
  }

  /** A running message's events as they come; a stream that breaks is asked again from the last
   * event seen (seen ones are skipped), a few times, then shown as broken. */
  private follow(id: string, turn: Turn, tries: number) {
    if (this.unsub.has(id)) return;
    const gen = this.gen;
    const sub = nextGen();
    const off = on("events:" + id, (b: any) => {
      if (b.gen && b.gen !== sub) return; // another subscription's
      if (b.events?.length) {
        tries = 0;
        turn.add(b.events as Ev[]);
        this.rev++;
        const cur = this.msgs.find(x => x.id === id);
        if (cur?.state === "queued") this.replace({ ...cur, state: "running" });
      }
      if (b.final) { this.replace(b.final, true); this.loadChats(); this.loadAgents(); }
      else if (b.error) this.lost(id, turn, off, b.error, tries, gen);
    });
    this.unsub.set(id, { off, sub });
    this.broken = without(this.broken, id);
    Node.Follow(id, turn.last, sub).then(
      // Another chat opened while it was asked: the stream the node just started is no one's.
      () => { if (this.unsub.get(id)?.sub !== sub) Node.Unfollow(id, sub); },
      e => this.lost(id, turn, off, e, tries, gen),
    );
  }

  /** Follows a message again once its stream was given up (the person asks). */
  followAgain(m: Msg) {
    this.broken = without(this.broken, m.id);
    this.watch(m);
  }

  private lost(id: string, turn: Turn, off: () => void, e: unknown, tries: number, gen: number) {
    off();
    if (this.unsub.get(id)?.off === off) this.unsub.delete(id);
    if (gen !== this.gen) return;
    if (lasting.has(errInfo(e).code) || tries + 1 >= followTries) {
      this.broken = { ...this.broken, [id]: errText(e) || t("The stream of this turn stopped") };
      return;
    }
    setTimeout(() => {
      const m = this.msgs.find(x => x.id === id);
      if (gen === this.gen && m && !final(m.state)) this.follow(id, turn, tries + 1);
    }, Math.min(30_000, 1000 * 2 ** tries));
  }

  /** A message as it is now. Final, its stream is let go: when its own last batch said so
   * (`streamed`), all of it came; when another answer did (Cancel's), the stream may still hold
   * its last batch (the app sends them a few times a second), so what it had not given is read. */
  replace(m: Msg, streamed = false) {
    const i = this.msgs.findIndex(x => x.id === m.id);
    if (i >= 0) this.msgs[i] = { ...this.msgs[i], ...m };
    else if (m.thread === this.thread) this.msgs = [...this.msgs, m];
    if (final(m.state)) {
      const f = this.unsub.get(m.id);
      if (f) {
        f.off();
        this.unsub.delete(m.id);
        const t = this.turns[m.id];
        if (!streamed && t) this.readEvents(m.id, t);
      }
      this.runningSince.delete(m.id);
    }
  }

  /** A setting chosen for an agent ("": its default). */
  setting(agent: string, k: keyof Settings): string {
    return this.settings[agent]?.[k] ?? "";
  }
  setSetting(agent: string, k: keyof Settings, v: string) {
    const cur = this.settings[agent] ?? { model: "", effort: "", mode: "" };
    this.settings = { ...this.settings, [agent]: { ...cur, [k]: v } };
  }

  /** The settings a message to this agent goes with: those chosen for it that it offers (an agent
   * of this Mac; another's options are not known). */
  settingsFor(agent: string): Partial<Settings> {
    if (!this.localAgents().some(a => a.address === agent)) return {};
    const opts = this.options[agent]?.options ?? [];
    const out: Partial<Settings> = {};
    for (const k of Object.keys(settingCats) as (keyof Settings)[]) {
      const v = this.setting(agent, k);
      const o = opts.find(o => o.category === settingCats[k]);
      if (v && o?.values.some(x => x.value === v)) out[k] = v;
    }
    return out;
  }

  // The composer's drafts, one per chat ("new": the new task's). A Map, not state: a draft is made
  // where it is first read (in a $derived), and a new task's draft moving to its thread bumps drafts.
  private draftMap = new Map<string, ComposerDraft>();
  /** Keys whose draft moved (a session's to its thread): an editor still on the old key gets it. */
  private draftAlias = new Map<string, string>();
  private drafts = $state(0);
  /** The key of the open chat's draft: its thread's, once it has one (a session or a cloud
   * conversation continued here becomes a thread), else the chat's, else the new task's. */
  draftKey(): string {
    return this.thread ? "thread:" + this.thread : this.chat?.key ?? "new";
  }

  /** A draft whose message made a thread becomes the thread's (wherever the screen is now); a
   * draft the thread has already keeps what it holds, and gets what was left of this one. */
  private moveDraft(d: ComposerDraft, to: string) {
    if (this.draftMap.get(to) === d) return;
    if (this.draftMap.get(d.key) === d) {
      this.draftMap.delete(d.key);
      if (d.key !== "new") this.draftAlias.set(d.key, to); // a new task's next draft is a new one
    }
    const there = this.draftMap.get(to);
    if (there && there !== d) {
      there.text = [there.text, d.text].filter(Boolean).join("\n");
      mergeFiles(there, d); // a request given back keeps its files first
      if (!there.op.pending() && d.op.pending()) there.op = d.op; // whole: its key with its sandbox
      d.forward(there);
    } else {
      d.key = to;
      this.draftMap.set(to, d);
    }
    this.drafts++;
  }
  /** Where a new task goes as the draft says now: the place, and this Mac's agent and directory or
   * the cloud's catalogue agent and repository. */
  destNow(): string {
    const d = this.draft;
    return d.where === "cloud" ? `cloud\u0000${d.cloudAgent}\u0000${d.repo}` : `mac\u0000${d.agent}\u0000${d.cwd}`;
  }

  /** Whether a draft still describes the message sent from it to `dest`. A chat's draft always
   * does (the chat is where it goes); a new task's while its place, agent, directory or repository
   * are as they were when it was sent. Every outcome of a send and every automatic link of a draft
   * asks this before it moves the draft, keeps its sandbox or key, or changes the screen. */
  stillFor(d: ComposerDraft, dest: string | undefined): boolean {
    if (d.key !== "new" || !dest) return true;
    // A draft a new task replaced is no one's editor now: it is still the task it sent (the new
    // task has a draft and settings of its own). Only the draft being edited follows the settings.
    if (this.draftMap.get("new") !== d) return true;
    return dest === this.destNow();
  }

  /** A thread the node made of a session or a cloud sandbox continued here, that conversation's
   * chat gone from the list (the answer to its first message may have been lost): its draft is the
   * thread's. While the conversation's own chat is still listed, its draft stays its own. */
  private linkDrafts(chats: Chat[]) {
    for (const c of chats) {
      if (c.kind !== "thread" || !c.thread) continue;
      const from: string[] = [];
      if (c.session) from.push(`session:${(c.session_agent || c.agent || "").replace(/^agent:/, "")}/${c.session}`);
      if (c.agent?.startsWith("agent:sb_")) from.push("cloud:" + c.agent.slice("agent:".length)); // a sandbox's thread
      for (const key of from) {
        const d = this.draftMap.get(key);
        if (!d || d.busy || this.chats.some(x => x.key === key) || !this.stillFor(d, d.sending?.dest)) continue;
        if (d.sending) d.sending.moved = true;
        this.moveDraft(d, "thread:" + c.thread);
      }
      // A new cloud task whose sandbox was made and whose message the node took (its answer lost):
      // the thread of that sandbox is the task's, and the new task's screen becomes it — while the
      // draft still describes that task (in the cloud, the same catalogue agent and repository).
      // Once the person chose another place, agent or repository, the draft is a new task of its
      // own: it stays, and lets that sandbox go. (A session's or cloud chat's draft has no such
      // choice: where it goes is the chat itself.)
      const n = this.draftMap.get("new");
      if (n && !n.busy && n.sending && n.sandbox && n.sandbox.agent === c.agent) {
        if (!this.stillFor(n, n.sending.dest)) {
          n.forget();
          continue;
        }
        const shown = !this.chat && !this.thread;
        n.sending.moved = true;
        this.moveDraft(n, "thread:" + c.thread);
        if (shown) { this.target = c.agent ?? ""; this.openThread(c.thread); }
      }
    }
  }

  draftOf(key = this.draftKey()): ComposerDraft {
    this.drafts;
    for (let i = 0; this.draftAlias.has(key) && i < 16; i++) key = this.draftAlias.get(key)!;
    let d = this.draftMap.get(key);
    if (!d) { d = new ComposerDraft(key); this.draftMap.set(key, d); }
    return d;
  }

  /** Sends a draft: what was sent leaves it once the node has it; what was typed meanwhile stays
   * (with the chat a new task becomes). True when sent. */
  async submit(d: ComposerDraft): Promise<boolean> {
    const sent = d.text;
    const body = sent.trim();
    // Words, or files alone (a screenshot says it): nothing at all is not a message.
    if (!d.hasContent || d.busy) return false;
    d.busy = true;
    // Where it goes is as it was when sent; files still being read (a paste, then Enter at once)
    // are part of it: waited for, not left for the next message.
    const at = this.sendAt(d), lim = at.limits, owned = sendFiles(d, lim);
    await owned.wait;
    const { files: sentFiles, refused } = owned.files();
    owned.done();
    let ok = false;
    if (refused) {
      // Nothing goes in part (by the limits of the click, against the send's own files): the
      // draft stays as it is, and why is said.
      this.error = refused.why === "read" ? t("Could not read {name}", { name: refused.file })
        : refused.why === "count" ? t("A message takes at most {n} files", { n: lim?.max_files ?? 0 })
        : t("The files are over the node's limit of {n} together", { n: bytes(lim?.max_files_size ?? 0) });
    } else if (body || sentFiles.length) { // files only, and none of them came to be: nothing to send
      ok = await this.send(body, sentFiles.map(({ name, type, data }) => ({ name, type, data })), d, { text: sent, files: sentFiles }, at);
    }
    d.busy = false;
    if (!ok) {
      // Not sent: all of it comes back to the draft, files by identity (one only the send had,
      // read when the composer was full, too). Over the composer's limits with what was attached
      // since, the draft says so; the person removes something, nothing is dropped.
      giveBack(d, refused ? owned.all() : sentFiles);
      if (this.limits && d.files.some((f, i) => overLimits(d.files.slice(0, i), f, this.limits))) {
        this.error = [this.error, t("The draft is over the node's limits: remove a file to send it")].filter(Boolean).join("\n");
      }
    }
    // Not sent, and a new task was started meanwhile (it has its own draft): what did not go is
    // put back into the new task's, not lost with the draft no screen shows.
    if (!ok && ![...this.draftMap.values()].includes(d)) {
      // The text comes back into the new task's draft: its key and sandbox go with it only if that
      // task goes where this one went.
      const dest = d.op.dest();
      if (dest && dest !== this.destNow()) d.forget();
      this.moveDraft(d, "new");
    }
    return ok;
  }

  /** Sends a message: true once the node has it, false if not (said in error; the draft keeps the
   * text to send again). The chat it was sent from gets it, whichever is open by then. */
  async send(body: string, files: { name: string; type: string; data: string }[], d = this.draftOf(),
    sent?: { text: string; files: Attachment[] }, at = this.sendAt(d)): Promise<boolean> {
    // What the message is for, as it was when sent: the screen may change while it goes.
    const { chat, thread, draft, epoch, dest } = at;
    // The draft is a thread's already (its conversation became one, the answer lost): what is
    // sent from it goes on in that thread, not to a new one.
    const into = !thread && at.key.startsWith("thread:") ? at.key.slice("thread:".length) : "";
    let to = at.target;
    try {
      let task: any = { body, files };
      if (at.limits && new TextEncoder().encode(body).length > at.limits.max_body) {
        this.error = t("The text is over the node's limit of {n}", { n: bytes(at.limits.max_body) });
        return false;
      }
      if (!chat && !thread) {
        // A new task: here or in the cloud.
        if (draft.where === "cloud") {
          if (!draft.cloudAgent) { this.error = t("Choose an agent of the cloud's catalogue"); return false; }
          to = await this.sandboxFor(d, draft, body, dest);
        } else {
          to = draft.agent;
          task.cwd = draft.cwd;
        }
      } else if (into) {
        task.thread = into;
      } else if (chat?.kind === "session" && !thread) {
        task.session = chat.session; // the conversation continues where it was
      } else if (thread) {
        task.thread = thread;
      }
      if (!to) { this.error = t("Choose an agent"); return false; }
      // The settings chosen when it was sent (a model picked while its file was read is the next one's).
      Object.assign(task, { to }, at.settings[to] ?? {}); // none taken: a sandbox (no settings of this Mac's)
      // The same message is all of it the same (files' bytes and types, settings): only then the same key.
      let what = JSON.stringify(task);
      const { to: _to, thread: _th, session: _se, cwd: _cwd, ...rest } = task;
      const content = JSON.stringify(rest);
      if (d.epoch !== epoch) return false; // a new task meanwhile: this one is not it
      // Its draft moved to the thread the node made of it (its answer lost): the same message is
      // the request as it was first made, with its key — the node gives back what it has.
      if (d.sending?.moved && d.sending.content === content) {
        task = { ...d.sending.task };
        what = d.sending.what;
      }
      if (d.sending?.what !== what) d.sending = { what, key: newId(), content, task: { ...task }, dest };
      const key = d.sending.key;
      task.idem = key;
      const m = (await Node.SendTask(task)) as any as Msg;
      if (d.sending?.key === key) d.forget();
      // The conversation it was sent from is still the one open (not the screen's generation: a
      // chat left and opened again is still it).
      // The draft may have been edited into another task while it went (another place, agent,
      // repository): then it stays the new task's, and the screen stays where it is.
      const same = this.stillFor(d, dest);
      const here = !this.thread && (chat ? this.chat?.key === chat.key : !this.chat && this.draftMap.get("new") === d && same);
      // What was sent leaves the draft, what was typed meanwhile stays; the draft is the thread's now.
      // A draft edited into another task keeps all it holds: it is that task, sent text and all.
      if (sent && same) {
        d.text = afterSent(d.text, sent.text);
        // Exactly the attachments it sent, by identity: the same file attached again meanwhile stays.
        const gone = new Set(sent.files.map(f => f.rid));
        d.files = d.files.filter(f => !gone.has(f.rid));
      }
      if (same) this.moveDraft(d, "thread:" + m.thread);
      else d.forget();
      if (here) {
        this.thread = m.thread;
        this.target = to;
        // A thread that was there before this message (the draft had moved to it): its earlier
        // turns are shown and followed too, not just this one.
        if (into) await this.openThread(m.thread);
      }
      // Shown wherever its thread is open, now (if it is opened later, the node's list has it).
      if (this.thread === m.thread) {
        // Sent again after its answer was lost, the node gives the message it has: one, not two.
        if (this.msgs.some(x => x.id === m.id)) this.replace(m);
        else this.msgs = [...this.msgs, m];
        this.focus = m.id;
        this.watch(m);
      }
      // The conversation keeps its folder as it becomes a thread.
      if (!thread && chat?.folder) await Node.MoveChat("thread:" + m.thread, chat.folder).catch(() => {});
      this.loadChats();
      return true;
    } catch (e) { this.error = errText(e); return false; }
  }

  /** What a message is for, taken when it is sent: the chat, thread and draft of then. */
  private sendAt(d: ComposerDraft) {
    // The settings chosen then, for each agent it can go to: the new task's agent of this Mac, or
    // the chat's (a cloud task's sandbox is known later, and has no settings of this Mac's).
    return {
      chat: this.chat, thread: this.thread, draft: { ...this.draft }, epoch: d.epoch, target: this.target,
      key: d.key, limits: this.limits, // the draft's chat, and the node's limits, as they were
      dest: d.key === "new" ? this.destNow() : "", // where this new task goes, as it was sent
      settings: Object.fromEntries([this.draft.agent, this.target].filter(Boolean).map(a => [a, this.settingsFor(a)])) as Record<string, Partial<Settings>>,
    };
  }

  private async sandboxFor(d: ComposerDraft, draft: Draft, body: string, what: string): Promise<string> {
    if (d.sandbox?.for === what) return d.sandbox.agent;
    const epoch = d.epoch;
    // Its request is frozen at the first try and kept with where it goes: the answer lost, the
    // retry is that request again, and gets the sandbox made then (a second is a second machine).
    const r = d.op.sandboxFor(what, () => ({ agent: draft.cloudAgent, repo: draft.repo, name: body.split("\n")[0].slice(0, 40) }));
    const sb: any = await Node.CreateCloudTask(r.agent, r.repo, r.name, r.idem);
    if (d.epoch === epoch) d.sandbox = { for: what, agent: sb.agent };
    return sb.agent;
  }

  /** A local chat sent on to the cloud: the cloud chat it goes on in. Each chat's unfinished
   * handoff is kept here (not in the form, which is made anew), its request frozen with its key at
   * the first try, until an answer comes. Asked again with the form as it was (restored from it,
   * pendingHandoff), the frozen request goes whole, and the node gives the sandbox and message of
   * the first. Only another cloud agent or note, chosen by the person, is another handoff. */
  private handoffs = new Map<string, HandoffReq & { idem: string }>();
  pendingHandoff(c: Chat | null): HandoffReq | undefined {
    return c ? this.handoffs.get(handoffFrom(c.kind === "thread" ? c.thread ?? "" : "", c.agent ?? "", c.session ?? "")) : undefined;
  }
  async handoff(req: HandoffReq): Promise<Chat> {
    const from = handoffFrom(req.thread, req.agent, req.session);
    let r = this.handoffs.get(from);
    if (!r || r.cloud_agent !== req.cloud_agent || r.note !== req.note) {
      r = { ...req, idem: newId() };
      this.handoffs.set(from, r);
    }
    const res: any = await Node.Handoff({ ...r } as any);
    if (this.handoffs.get(from) === r) this.handoffs.delete(from);
    await this.loadChats();
    return this.chats.find(x => x.key === res.chat.key) ?? res.chat;
  }

  async answer(msg: string, req: string, option: string) {
    try { await Node.Answer(msg, req, option); } catch (e) { this.error = errText(e); }
  }

  /** The open chat's turns that run now. */
  runningHere() {
    return this.msgs.filter(m => m.state === "running");
  }
  /** Stops what runs in the open chat (what waits in the queue goes on after). */
  async stopHere() {
    await Promise.all(this.runningHere().map(m => this.cancel(m.id)));
  }

  async cancel(id: string) {
    try { this.replace((await Node.Cancel(id)) as any); } catch (e) { this.error = errText(e); }
  }

  async loadPlaybooks() {
    try { this.playbooks = ((await Node.Playbooks()) ?? []) as any; } catch { this.playbooks = []; }
  }

  async renameChat(chat: string, title: string) {
    const c = this.chats.find(x => x.key === chat);
    if (c && title.trim()) c.title = title.trim();
    try { await Node.SetChatTitle(chat, title); await this.loadChats(); } catch (e) { this.error = errText(e); }
  }

  /** Forks the open chat's agent session into a new chat (the conversation so far, to go on apart). */
  async forkChat() {
    const c = this.chat;
    // A thread's session is the agent's that runs it (its directory looked up by the node).
    const agent = c?.session_agent || c?.agent || this.target;
    if (!c?.session || !agent || c.kind === "cloud") return;
    try {
      // The directory: a session chat's own; a thread's when the session is its first agent's (whose
      // directory it is), else looked up by the node.
      const dir = c.kind === "session" || agent === c.agent ? c.cwd ?? "" : "";
      const sid = await Node.ForkSession(agent, c.session, dir);
      const name = agent.replace(/^agent:/, "");
      const key = `session:${name}/${sid}`;
      await Node.SetChatTitle(key, "Fork: " + (c.title || "chat").slice(0, 80)).catch(() => {});
      await this.loadChats();
      const fork = this.chats.find(x => x.key === key) ??
        ({ key, kind: "session", agent, session: sid, cwd: c.cwd, title: "Fork: " + c.title, updated: new Date().toISOString() } as any as Chat);
      await this.open(fork);
    } catch (e) { this.error = errText(e); }
  }

  /** Opens a chat by its thread (the board's cards, the palette). */
  openKey(key: string) {
    const c = this.chats.find(x => x.key === key);
    if (c) this.open(c);
  }

  // Folders.
  async newFolder(name: string) {
    try { await Node.CreateFolder(name); await this.loadChats(); } catch (e) { this.error = errText(e); }
  }
  async renameFolder(id: string, name: string) {
    try { await Node.RenameFolder(id, name); await this.loadChats(); } catch (e) { this.error = errText(e); }
  }
  async deleteFolder(id: string) {
    try { await Node.DeleteFolder(id); await this.loadChats(); } catch (e) { this.error = errText(e); }
  }
  async moveChat(chat: string, folder: string) {
    const c = this.chats.find(x => x.key === chat);
    if (c) c.folder = folder || undefined; // at once; the list follows
    try { await Node.MoveChat(chat, folder); await this.loadChats(); } catch (e) { this.error = errText(e); }
  }

  /** The preview: what its page did (problems, its address), kept across tabs. */
  preview = $state<{ events: PreviewEvent[]; url: string; title: string; loaded: boolean; pending: string; cleared: boolean; clearedAt: number; clearedUrl: string; loadAt: number }>({ events: [], url: "", title: "", loaded: false, pending: "", cleared: false, clearedAt: 0, clearedUrl: "", loadAt: 0 });
  /** A page of this Mac (a dev server) opened in the app's preview, not the system's browser. */
  openInPreview(url: string) {
    this.preview.pending = url;
    this.preview.loaded = true;
    if (this.view !== "chat") this.view = "chat";
    this.openTab("preview");
  }
  /** The error shown now (App shows it; the preview hides under it — a native view would cover it). */
  toast = $state("");

  /** Files to attach to the next message (the preview's problems): the composer takes them. */
  pendingFiles = $state<{ name: string; type: string; size: number; data: string }[]>([]);

  /** The artifact shown, and those opened in this chat. */
  artifact = $state<ArtifactRef | null>(null);
  openedArtifacts = $state<ArtifactRef[]>([]);
  openArtifact(a: ArtifactRef) {
    const same = (x: ArtifactRef) => x.msg === a.msg && x.name === a.name && x.path === a.path && x.base === a.base;
    if (!this.openedArtifacts.some(same)) this.openedArtifacts = [...this.openedArtifacts, a];
    this.artifact = a;
    if (this.view !== "chat") this.view = "chat";
    this.openTab("artifacts");
  }
  /** The preview's problems cleared: the page is not fixed by it (a reload shows them again). */
  clearPreview() {
    const p = this.preview;
    p.events = []; p.cleared = true; p.clearedAt = Date.now(); p.clearedUrl = p.url;
  }
  /** The preview's page loaded again (fresh: its problems listed anew). */
  previewReload(fresh = false) {
    if (fresh) this.preview.events = [];
    this.preview.loadAt = Date.now();
    Node.PreviewReload().catch(e => (this.error = errText(e)));
  }
  previewBack() {
    this.preview.loadAt = Date.now();
    Node.PreviewBack().catch(e => (this.error = errText(e)));
  }
  notePreview(e: PreviewEvent) {
    // A page loaded that was asked for after the list was cleared (a reload, another address): what it
    // shows from now on is the truth. A load already under way when it was cleared changes nothing.
    if (e.kind === "nav") {
      if (e.url) { this.preview.url = e.url; this.preview.title = e.title ?? ""; }
      // (or another page than the one cleared: a link followed in it, Back)
      if (this.preview.cleared && !this.preview.clearedUrl && e.url && this.preview.loadAt <= this.preview.clearedAt) {
        this.preview.clearedUrl = e.url; // cleared during the first load: that page is the one cleared
      } else if (this.preview.loadAt > this.preview.clearedAt || (e.url && this.preview.clearedUrl && e.url !== this.preview.clearedUrl)) {
        this.preview.cleared = false;
      }
      return;
    }
    this.preview.cleared = false;
    this.preview.events = [...this.preview.events.slice(-299), e];
  }

  /** The size of everything, as a factor (the app's zoom): 0.85 to 1.5. */
  textSize = $state(1);
  static sizes = [0.85, 0.92, 1, 1.1, 1.2, 1.35, 1.5];
  setTextSize(z: number) {
    this.textSize = Math.min(1.5, Math.max(0.85, z));
    (document.documentElement.style as any).zoom = String(this.textSize);
    try { localStorage.setItem("text-size", String(this.textSize)); } catch {}
  }
  stepTextSize(dir: 1 | -1) {
    const s = App.sizes;
    const i = s.findIndex(v => v >= this.textSize - 0.001);
    this.setTextSize(s[Math.min(s.length - 1, Math.max(0, (i < 0 ? s.length - 1 : i) + dir))]);
  }

  setTheme(t: "light" | "dark" | "system") {
    this.theme = t;
    try { localStorage.setItem("theme", t); } catch {}
    applyTheme(t);
  }

  /** The agent of the focused message, when it is of this node (its directory is the panel's). */
  focusAgent(): string {
    const m = this.msgs.find(x => x.id === this.focus) ?? this.msgs[this.msgs.length - 1];
    const to = m?.to ?? this.target;
    return to?.startsWith("agent:") && !to.includes("@") && !to.startsWith("agent:sb_") ? to : "";
  }

  /** The directory of the open chat (its task's, or its session's). */
  chatCwd(): string {
    const m = this.msgs[0];
    return m?.meta?.acp_cwd || m?.meta?.acp_session_cwd || this.chat?.cwd || "";
  }
}

/** Which chat a handoff is of: its thread, or its session. */
function handoffFrom(thread: string, agent: string, session: string) {
  return thread ? "thread:" + thread : `session:${agent}/${session}`;
}

export type HandoffReq = { thread: string; agent: string; session: string; cwd: string; cloud_agent: string; note: string };

/** A replayed conversation split at its user's messages. */
const stage: Record<string, number> = { queued: 0, running: 1, done: 2, failed: 2, cancelled: 2, expired: 2 };

/** A thread's messages as loaded, with those the screen already has: one per id, the later of the
 * two (by updated_at, then by state: a finished one is past a running one), in the loaded order
 * and then those the load did not have yet. */
/** A subscription's generation: rising, also across reloads of the window (from the clock). */
let lastGen = 0;
function nextGen(): number {
  lastGen = Math.max(lastGen + 1, Date.now() * 1000);
  return lastGen;
}

export function mergeMsgs(loaded: Msg[], shown: Msg[]): Msg[] {
  const byId = new Map(shown.map(m => [m.id, m]));
  const later = (a: Msg, b: Msg) => {
    const ta = Date.parse(a.updated_at) || 0, tb = Date.parse(b.updated_at) || 0;
    if (ta !== tb) return ta > tb ? a : b;
    return (stage[a.state] ?? 0) >= (stage[b.state] ?? 0) ? a : b;
  };
  const out = loaded.map(m => { const s = byId.get(m.id); return s ? later(m, s) : m; });
  const have = new Set(loaded.map(m => m.id));
  for (const m of shown) if (!have.has(m.id)) out.push(m);
  return out;
}

function without<T>(o: Record<string, T>, k: string): Record<string, T> {
  if (!(k in o)) return o;
  const { [k]: _, ...rest } = o;
  return rest;
}

function segments(evs: Ev[]): Segment[] {
  const out: Segment[] = [];
  let cur: Segment | null = null;
  for (const e of evs) {
    if (e.kind === "user") {
      let text = "";
      try { text = JSON.parse(e.data)?.text ?? ""; } catch {}
      if (cur && !cur.turn.blocks.length) { cur.user += text; continue; }
      cur = { user: text, turn: new Turn() };
      out.push(cur);
      continue;
    }
    if (!cur) { cur = { user: "", turn: new Turn() }; out.push(cur); }
    cur.turn.add([{ ...e, seq: cur.turn.last + 1 }]);
  }
  // A prompt acpost sent itself: what the person wrote, not what the node put before it.
  for (const s of out) s.user = ownBody(s.user);
  return out;
}

export function applyTheme(t: string) {
  const dark = t === "dark" || (t === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
}

export const app = new App();
