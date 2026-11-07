import { Node, on, errText } from "./api";
import { Turn, type Ev } from "./turn";

export type Msg = {
  id: string; from: string; to: string; thread: string; parent?: string; depth: number; state: string;
  body?: string; reply?: string; error?: string; error_code?: string; hint?: string;
  created_at: string; updated_at: string; finished_at?: string; files?: { name: string; type: string; size: number; reply?: boolean }[];
};
export type Agent = { name: string; address: string; driver: string; node?: string; queued: number; running: number; process?: boolean };
export type Conv = { thread: string; title: string; agents: string[]; messages: number; running: number; queued: number; failed: number; started: string; updated: string };

export const final = (s: string) => s === "done" || s === "failed" || s === "cancelled" || s === "expired";

class App {
  conn = $state<{ ok: boolean; error?: string; url?: string; node?: string; you?: string } | null>(null);
  agents = $state<Agent[]>([]);
  convs = $state<Conv[]>([]);
  convNext = $state("");
  thread = $state<string>("");            // the open conversation ("" = a new one)
  msgs = $state<Msg[]>([]);
  turns = $state<Record<string, Turn>>({});
  rev = $state(0);                        // bumps when a turn changes
  focus = $state<string>("");             // the message the right panel is about
  tab = $state<"activity" | "changes" | "terminal" | "prs" | "usage" | "preview">("activity");
  panel = $state(true);
  view = $state<"chat" | "usage">("chat");
  theme = $state<"light" | "dark" | "system">("light");
  target = $state<string>("");            // the agent a new message goes to
  error = $state("");
  private unsub = new Map<string, () => void>();

  async connect() {
    const c: any = await Node.Status();
    this.conn = { ok: c.ok, error: c.error, url: c.url, node: c.status?.node, you: c.status?.you };
    if (!c.ok) return;
    await Promise.all([this.loadAgents(), this.loadConvs()]);
    if (!this.target) this.target = this.agents.find(a => a.driver !== "inbox")?.address ?? this.agents[0]?.address ?? "";
  }

  async loadAgents() {
    try { this.agents = (await Node.Agents()) as any; } catch (e) { this.error = errText(e); }
  }

  async loadConvs(more = false) {
    try {
      const p: any = await Node.Conversations(more ? this.convNext : "");
      this.convs = more ? [...this.convs, ...p.items] : p.items ?? [];
      this.convNext = p.next ?? "";
    } catch (e) { this.error = errText(e); }
  }

  async open(thread: string) {
    for (const [id, stop] of this.unsub) { stop(); Node.Unfollow(id); }
    this.unsub.clear();
    this.thread = thread;
    this.msgs = [];
    this.turns = {};
    this.focus = "";
    this.view = "chat";
    if (!thread) return;
    try {
      const ms = ((await Node.Thread(thread)) ?? []) as any as Msg[];
      if (this.thread !== thread) return;
      this.msgs = ms;
      for (const m of ms) this.watch(m);
      this.focus = ms[ms.length - 1]?.id ?? "";
      const to = ms.find(m => m.to.startsWith("agent:"))?.to;
      if (to) this.target = to;
    } catch (e) { this.error = errText(e); }
  }

  /** Reads a message's events, and follows them while it runs. */
  watch(m: Msg) {
    if (!m.to.startsWith("agent:")) return;
    const t = (this.turns[m.id] ??= new Turn());
    const apply = (b: any) => {
      if (b.events?.length) {
        t.add(b.events as Ev[]);
        this.rev++;
        const cur = this.msgs.find(x => x.id === m.id);
        if (cur?.state === "queued") this.replace({ ...cur, state: "running" }); // its turn has begun
      }
      if (b.final) { this.replace(b.final); this.loadConvs(); this.loadAgents(); }
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

  async send(body: string, files: { name: string; type: string; data: string }[]) {
    if (!this.target) { this.error = "choose an agent"; return; }
    try {
      const m = (await Node.Send(this.target, body, this.thread, files as any)) as any as Msg;
      if (!this.thread) {
        this.thread = m.thread;
        this.msgs = [m];
      } else this.msgs = [...this.msgs, m];
      this.focus = m.id;
      this.watch(m);
      this.loadConvs();
    } catch (e) { this.error = errText(e); }
  }

  async answer(msg: string, req: string, option: string) {
    try { await Node.Answer(msg, req, option); } catch (e) { this.error = errText(e); }
  }

  async cancel(id: string) {
    try { this.replace((await Node.Cancel(id)) as any); } catch (e) { this.error = errText(e); }
  }

  setTheme(t: "light" | "dark" | "system") {
    this.theme = t;
    try { localStorage.setItem("theme", t); } catch {}
    applyTheme(t);
  }

  /** The agent of the focused message (its working directory is the panel's). */
  focusAgent(): string {
    const m = this.msgs.find(x => x.id === this.focus) ?? this.msgs[this.msgs.length - 1];
    const to = m?.to ?? this.target;
    return to?.startsWith("agent:") && !to.includes("@") ? to : "";
  }
}

export function applyTheme(t: string) {
  const dark = t === "dark" || (t === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
}

export const app = new App();
