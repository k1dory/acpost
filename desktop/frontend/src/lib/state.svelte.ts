import { t, tr } from "./i18n.svelte";
import { Node, on, errText, errInfo } from "./api";
import { Turn, type Ev } from "./turn";

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

/** Where a new task runs, which agent, in which directory, with which settings. */
export type Draft = { where: "mac" | "cloud"; agent: string; cloudAgent: string; cwd: string; repo: string; model: string; effort: string; mode: string };

export type PreviewEvent = { kind: string; message?: string; source?: string; line?: number; col?: number; stack?: string; url?: string; title?: string; main_frame?: boolean };
export type PanelTab = "activity" | "changes" | "terminal" | "prs" | "preview" | "artifacts";
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
    this.panel = true;
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
  panel = $state(true);
  view = $state<"chat" | "usage" | "settings" | "board" | "wiki">("chat");
  playbooks = $state<{ name: string; body: string }[]>([]);
  quote = $state("");     // text to put in the composer (⌘L)
  palette = $state(false); // ⌘K
  asking = $state<any[]>([]); // approvals waiting
  theme = $state<"light" | "dark" | "system">("light");
  target = $state<string>("");
  draft = $state<Draft>({ where: "mac", agent: "agent:claude", cloudAgent: "", cwd: "", repo: "", model: "", effort: "", mode: "" });
  options = $state<Record<string, Options>>({});
  cloudAgents = $state<CloudAgent[]>([]);
  home = $state("");
  error = $state("");
  private unsub = new Map<string, () => void>();

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
    const c: any = await Node.Status();
    this.conn = { ok: c.ok, error: c.error, url: c.url, node: c.status?.node, you: c.status?.you };
    if (!c.ok) return;
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
    try { this.agents = (await Node.Agents()) as any; } catch (e) { this.error = errText(e); }
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
  private asked = new Set<string>();

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
      for (const r of reqs) {
        const id = r.request_id ?? r.id;
        if (id && !this.asked.has(id)) { this.asked.add(id); this.notify(t("Needs approval"), r.title || t("An agent asks")); }
      }
    } catch {}
  }

  async loadChats() {
    this.loadingChats = true;
    try {
      const c: any = await Node.Chats();
      // A thread that ran and does not now: its turn ended.
      for (const ch of (c.items ?? []) as Chat[]) {
        if (ch.kind !== "thread") continue;
        const was = this.wasRunning.get(ch.key) ?? 0;
        if (was > 0 && !ch.running && !ch.queued) this.notify(ch.failed ? t("Failed") : t("Done"), ch.title);
        this.wasRunning.set(ch.key, (ch.running ?? 0) + (ch.queued ?? 0));
      }
      this.chats = c.items ?? [];
      try { localStorage.setItem(this.chatsKey(), JSON.stringify({ items: this.chats, folders: c.folders ?? [] })); } catch {}
      // A new task's thread, once listed, is the open chat (its session, to fork, comes with it).
      if (this.thread && (!this.chat || this.chat.key === "thread:" + this.thread)) {
        const mine = this.chats.find(x => x.key === "thread:" + this.thread);
        if (mine) this.chat = mine;
      }
      this.folders = c.folders ?? [];
      this.partial = c.partial ?? [];
    } catch (e) { this.error = errText(e); }
    this.loadingChats = false;
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
    this.artifact = null; // artifacts are the chat's
    this.openedArtifacts = [];
    for (const [id, stop] of this.unsub) { stop(); Node.Unfollow(id); }
    this.unsub.clear();
    this.msgs = [];
    this.history = [];
    this.turns = {};
    this.focus = "";
    this.thread = "";
    this.view = "chat";
  }

  /** A new task. */
  newTask() {
    this.reset();
    this.chat = null;
  }

  async open(chat: Chat) {
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
    } catch (e) { this.error = errText(e); }
    this.historyLoading = false;
    this.loadOptions(this.target);
  }

  private async openThread(thread: string) {
    this.thread = thread;
    try {
      const ms = ((await Node.Thread(thread)) ?? []) as any as Msg[];
      if (this.thread !== thread) return;
      this.msgs = ms;
      for (const m of ms) this.watch(m);
      this.focus = ms[ms.length - 1]?.id ?? "";
      const to = ms.find(m => m.to.startsWith("agent:"))?.to;
      if (to) this.target = to;
      this.loadOptions(this.target);
    } catch (e) { this.error = errText(e); }
  }

  watch(m: Msg) {
    if (!m.to.startsWith("agent:")) return;
    const t = (this.turns[m.id] ??= new Turn());
    const apply = (b: any) => {
      if (b.events?.length) {
        t.add(b.events as Ev[]);
        this.rev++;
        const cur = this.msgs.find(x => x.id === m.id);
        if (cur?.state === "queued") this.replace({ ...cur, state: "running" });
      }
      if (b.final) { this.replace(b.final); this.loadChats(); this.loadAgents(); }
      if (b.error) this.error = b.error;
    };
    if (final(m.state)) {
      Node.Events(m.id, 0).then(evs => { t.add((evs ?? []) as any); this.rev++; }).catch(() => {});
      return;
    }
    if (this.unsub.has(m.id)) return;
    this.unsub.set(m.id, on("events:" + m.id, apply));
    Node.Follow(m.id, t.last).catch(e => (this.error = errText(e)));
  }

  replace(m: Msg) {
    const i = this.msgs.findIndex(x => x.id === m.id);
    if (i >= 0) this.msgs[i] = { ...this.msgs[i], ...m };
    else if (m.thread === this.thread) this.msgs = [...this.msgs, m];
    if (final(m.state)) { this.unsub.get(m.id)?.(); this.unsub.delete(m.id); }
  }

  /** The settings the composer sends with (the draft's, for an agent of this Mac). */
  settingsFor(agent: string) {
    if (!this.localAgents().some(a => a.address === agent)) return {};
    return { model: this.draft.model, effort: this.draft.effort, mode: this.draft.mode };
  }

  async send(body: string, files: { name: string; type: string; data: string }[]) {
    try {
      let to = this.target;
      const task: any = { body, files };
      if (!this.chat && !this.thread) {
        // A new task: here or in the cloud.
        if (this.draft.where === "cloud") {
          if (!this.draft.cloudAgent) { this.error = "choose an agent of the cloud's catalogue"; return; }
          const sb: any = await Node.CreateCloudTask(this.draft.cloudAgent, this.draft.repo, body.split("\n")[0].slice(0, 40));
          to = sb.agent;
        } else {
          to = this.draft.agent;
          task.cwd = this.draft.cwd;
        }
      } else if (this.chat?.kind === "session" && !this.thread) {
        task.session = this.chat.session; // the conversation continues where it was
      } else if (this.thread) {
        task.thread = this.thread;
      }
      if (!to) { this.error = "choose an agent"; return; }
      Object.assign(task, { to }, this.settingsFor(to));
      const m = (await Node.SendTask(task)) as any as Msg;
      const was = this.chat;
      if (!this.thread) {
        this.thread = m.thread;
        this.target = to;
        // The conversation keeps its folder as it becomes a thread.
        if (was?.folder) await Node.MoveChat("thread:" + m.thread, was.folder).catch(() => {});
      }
      this.msgs = [...this.msgs, m];
      this.focus = m.id;
      this.watch(m);
      this.loadChats();
    } catch (e) { this.error = errText(e); }
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

/** A replayed conversation split at its user's messages. */
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
  return out;
}

export function applyTheme(t: string) {
  const dark = t === "dark" || (t === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
}

export const app = new App();
