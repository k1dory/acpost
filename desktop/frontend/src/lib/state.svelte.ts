import { Node, on, errText } from "./api";
import { Turn, type Ev } from "./turn";

export type Msg = {
  id: string; from: string; to: string; thread: string; parent?: string; depth: number; state: string;
  body?: string; reply?: string; error?: string; error_code?: string; hint?: string; meta?: Record<string, string>;
  created_at: string; updated_at: string; finished_at?: string; files?: { name: string; type: string; size: number; reply?: boolean }[];
};
export type Agent = { name: string; address: string; driver: string; node?: string; queued: number; running: number; process?: boolean };
export type Chat = {
  key: string; kind: "thread" | "session" | "cloud"; agent?: string; title: string; cwd?: string; repo?: string;
  thread?: string; session?: string; sandbox?: string; status?: string; updated: string;
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
  tab = $state<"activity" | "changes" | "terminal" | "prs" | "usage" | "preview">("activity");
  panel = $state(true);
  view = $state<"chat" | "usage">("chat");
  theme = $state<"light" | "dark" | "system">("light");
  target = $state<string>("");
  draft = $state<Draft>({ where: "mac", agent: "agent:claude", cloudAgent: "", cwd: "", repo: "", model: "", effort: "", mode: "" });
  options = $state<Record<string, Options>>({});
  cloudAgents = $state<CloudAgent[]>([]);
  home = $state("");
  error = $state("");
  private unsub = new Map<string, () => void>();

  async connect() {
    const c: any = await Node.Status();
    this.conn = { ok: c.ok, error: c.error, url: c.url, node: c.status?.node, you: c.status?.you };
    if (!c.ok) return;
    this.home = await Node.Home();
    if (!this.draft.cwd) this.draft.cwd = this.home;
    await Promise.all([this.loadAgents(), this.loadChats(), this.loadCloudAgents()]);
    const local = this.localAgents();
    if (!local.some(a => a.address === this.draft.agent) && local[0]) this.draft.agent = local[0].address;
    this.loadOptions(this.draft.agent);
  }

  localAgents() { return this.agents.filter(a => a.driver === "acp"); }

  async loadAgents() {
    try { this.agents = (await Node.Agents()) as any; } catch (e) { this.error = errText(e); }
  }

  async loadCloudAgents() {
    try {
      this.cloudAgents = ((await Node.CloudAgents()) ?? []) as any;
      if (!this.draft.cloudAgent && this.cloudAgents[0]) this.draft.cloudAgent = this.cloudAgents[0].ref;
    } catch { this.cloudAgents = []; }
  }

  async loadChats() {
    this.loadingChats = true;
    try {
      const c: any = await Node.Chats();
      this.chats = c.items ?? [];
      this.folders = c.folders ?? [];
      this.partial = c.partial ?? [];
    } catch (e) { this.error = errText(e); }
    this.loadingChats = false;
  }

  async loadOptions(agent: string) {
    if (!agent || this.options[agent] || !this.localAgents().some(a => a.address === agent)) return;
    try { this.options = { ...this.options, [agent]: (await Node.Options(agent)) as any }; } catch { /* the agent may not start: no choices then */ }
  }

  /** Recent directories of the user's chats, newest first. */
  recentDirs(): string[] {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const c of this.chats) if (c.cwd && !seen.has(c.cwd)) { seen.add(c.cwd); out.push(c.cwd); }
    return out.slice(0, 12);
  }

  private reset() {
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

  async cancel(id: string) {
    try { this.replace((await Node.Cancel(id)) as any); } catch (e) { this.error = errText(e); }
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
