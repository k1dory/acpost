// A command the person ran from the composer ("!git status"), as the agent is told of it: one
// message with the command, what it printed and how it ended. The chat shows that message as the
// command and its end, the output folded (shellNote reads it back).

export type ShellEnd = { code?: number; stopped?: boolean; timedOut?: boolean; error?: string; cut?: boolean };
export type ShellNote = { cmd: string; output: string; end: string; failed: boolean };

/** The most of a command's output an agent is sent: its beginning and, mostly, its end (where a
 * failure says why). The node's own limit on a message's text is kept too. */
export const shellSent = 64 << 10;

const enc = new TextEncoder(), dec = new TextDecoder();
const size = (s: string) => enc.encode(s).length;

/** `out` in at most `max` bytes: a quarter from its start, the rest from its end, the gap said. */
export function fitOutput(out: string, max: number): string {
  const b = enc.encode(out);
  if (b.length <= max) return out;
  if (max < 64) return ""; // no room for any of it and the line that says so
  const room = max - 64, head = Math.floor(room / 4), tail = room - head;
  // Cut at a line where there is one near, never inside a character.
  const first = dec.decode(b.slice(0, head)).replace(/�+$/, ""), last = dec.decode(b.slice(b.length - tail)).replace(/^�+/, "");
  const a = first.lastIndexOf("\n") > first.length / 2 ? first.slice(0, first.lastIndexOf("\n") + 1) : first;
  const z = last.indexOf("\n") >= 0 && last.indexOf("\n") < last.length / 2 ? last.slice(last.indexOf("\n") + 1) : last;
  return `${a}${a.endsWith("\n") || !a ? "" : "\n"}[… ${b.length - size(a) - size(z)} bytes left out …]\n${z}`;
}

/** The message that tells an agent of a run: "$ command", its output fenced, how it ended when
 * that is not plainly well. `max`: the node's limit on a message's text (0: none). */
export function shellBody(cmd: string, output: string, end: ShellEnd = {}, max = 0): string {
  const how = end.error ? `(not run: ${end.error.replace(/\s+/g, " ")})` : end.stopped ? "(stopped)" : end.timedOut ? "(ran too long, ended)"
    : end.code ? `(exit ${end.code})` : "";
  const whole = output.replace(/\s+$/, "");
  const make = (limit: number) => {
    const out = fitOutput(whole, limit);
    // A fence no line of the command or the output is: where the output begins and ends is certain.
    const fence = "`".repeat(Math.max(3, ...[...(cmd + "\n" + out).matchAll(/`+/g)].map(m => m[0].length + 1)));
    return `$ ${cmd}\n` + (end.error && !out ? "" : `${fence}\n${out}\n${fence}\n`) + (end.cut ? "(some of the output was not kept)\n" : "") + how;
  };
  // The whole message is within the node's limit, its fences (as long as the output makes them) and
  // its last line counted: what is over comes off the output.
  // (Cut at lines, a smaller limit is not always a smaller output: a line's worth more comes off each time.)
  let limit = shellSent, body = make(limit);
  for (let i = 0; max > 0 && size(body) > max && i < 8; i++) body = make(limit = Math.max(0, Math.min(limit, size(whole)) - (size(body) - max) - 64));
  return max > 0 && size(body) > max ? make(0) : body;
}

/** A message's text read back as a run, or null when it is not one (a message of words). */
export function shellNote(body: string | undefined): ShellNote | null {
  if (!body?.startsWith("$ ")) return null;
  let rest = body.replace(/\s+$/, ""), end = "";
  const how = /\n\((exit -?\d+|stopped|ran too long, ended|not run: [^\n]*)\)$/.exec(rest);
  if (how) { end = how[1]; rest = rest.slice(0, how.index); }
  rest = rest.replace(/\n\((?:the rest|some) of the output was not kept\)$/, "");
  const failed = !!end;
  // Not run at all: the command alone.
  if (end.startsWith("not run") && !/\n`{3,}$/.test(rest)) return { cmd: rest.slice(2), output: "", end, failed };
  const close = /\n(`{3,})$/.exec(rest);
  if (!close) return null;
  const open = rest.indexOf("\n" + close[1] + "\n");
  if (open < 0 || open + close[1].length + 1 > close.index) return null;
  return { cmd: rest.slice(2, open), output: rest.slice(open + close[1].length + 2, close.index), end, failed };
}
