import { newId } from "./ids";

/** An operation's idempotency key: made before its first request and kept with what it asks, so
 * a retry after a lost answer is the same operation (the node gives back what the first made).
 * Asking something else is another operation, with another key; done() lets it go. */
export class OpKey {
  private cur: { what: string; key: string } | null = null;

  /** The key for `what` (compared as JSON): the same while it asks the same. */
  for(what: unknown): string {
    const w = JSON.stringify(what);
    if (this.cur?.what !== w) this.cur = { what: w, key: newId() };
    return this.cur.key;
  }

  /** The operation is over (its answer came): the next is a new one. With a key, only if it is
   * still that operation's. */
  done(key?: string) {
    if (key === undefined || this.cur?.key === key) this.cur = null;
  }
}
