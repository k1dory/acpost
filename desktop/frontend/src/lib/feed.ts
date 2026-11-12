// A message's events as a trace reads them while it may still run: its history read from the node
// page after page, and what its stream brings meanwhile. The history read is what counts: a batch
// that comes before the read reaches it waits (a live batch must not make the read's older answer
// look stale, nor skip what lies between), and is applied once the events before it are there.

import type { Ev } from "./turn";

export class EventFeed {
  /** The events, in order, with none missing before the last. */
  raw: Ev[] = [];
  /** The last event of that unbroken history. */
  last = 0;
  private pending = new Map<number, Ev>();
  private reading: Promise<void> | null = null;
  private again = false;
  private stopped = false;

  /** read: the message's events after a seq (one page); changed: events were added (in order). */
  constructor(private read: (after: number) => Promise<Ev[]>, private changed: (fresh: Ev[]) => void) {}

  /** Reads what the node has after the history so far, to its end. Asked while a read is going,
   * that read goes on once more when it is done. */
  sync(): Promise<void> {
    if (this.stopped) return Promise.resolve();
    if (this.reading) { this.again = true; return this.reading; }
    this.reading = (async () => {
      try {
        do {
          this.again = false;
          for (;;) {
            const from = this.last;
            const page = (await this.read(from)) ?? [];
            if (this.stopped) return;
            // A page is every event after `from`, up to its size: what of it is after `last` is
            // the history's next part (a live batch may have filled some of it meanwhile).
            this.add(page.filter(e => e.seq > this.last).sort((a, b) => a.seq - b.seq));
            if (!page.length || this.last <= from) break;
          }
        } while (this.again && !this.stopped);
      } finally { this.reading = null; }
    })();
    return this.reading;
  }

  /** Events the stream brought: applied once the history reaches them; one that leaves a gap
   * makes the history be read (what lies between is the node's). */
  live(evs: Ev[]) {
    if (this.stopped) return;
    for (const e of evs) if (e.seq > this.last) this.pending.set(e.seq, e);
    this.add([]);
    if (this.pending.size) void this.sync().catch(() => {});
  }

  /** Nothing more is read or applied (another turn is shown). */
  stop() { this.stopped = true; this.pending.clear(); }

  private add(fresh: Ev[]) {
    const out = [...fresh];
    if (fresh.length) this.last = fresh[fresh.length - 1].seq;
    for (const s of [...this.pending.keys()]) if (s <= this.last) this.pending.delete(s);
    for (let e = this.pending.get(this.last + 1); e; e = this.pending.get(this.last + 1)) {
      this.pending.delete(e.seq);
      out.push(e);
      this.last = e.seq;
    }
    if (!out.length) return;
    this.raw.push(...out);
    this.changed(out);
  }
}
