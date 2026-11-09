// A native view laid over the window (the preview), driven from the interface. Each binding call
// runs in its own goroutine, so two calls sent at once may reach the view in either order (a Show
// sent before a Hide could come after it, and the view stay up over the interface). Here its
// calls go one at a time, and the state asked last wins: one asked while a call is out replaces
// any not sent yet. A call returns once the native side queued it (on the main thread, in order),
// so the view ends as asked last. (components/idePane.ts does the same for the IDE's pane.)

export type Owner<S> = {
  /** Asks the view to be s: true once applied, false when a later state took its place. */
  set(s: S): Promise<boolean>;
  /** The owner gone (its component destroyed): the view goes to its hidden state unless a newer
   * owner has it, and nothing this one asks after is sent. */
  close(): Promise<boolean>;
};

type Want<S> = { s: S; done: (applied: boolean) => void; fail: (e: unknown) => void };

/** One view's queue: apply(s) makes the view s. */
export class Surface<S> {
  #want: Want<S> | null = null;
  #busy = false;
  constructor(private apply: (s: S) => Promise<unknown>) {}

  set(s: S): Promise<boolean> {
    return new Promise((done, fail) => {
      this.#want?.done(false);
      this.#want = { s, done, fail };
      void this.#pump();
    });
  }

  async #pump() {
    if (this.#busy) return;
    this.#busy = true;
    while (this.#want) {
      const w = this.#want;
      this.#want = null;
      try { await this.apply(w.s); w.done(true); } catch (e) { w.fail(e); }
    }
    this.#busy = false;
  }
}

/** Owners of one view over one queue: the latest owner is the view's; an older one's calls are
 * dropped (a component leaving must not hide the one that came after it). */
export function surfaceOwners<S>(apply: (s: S) => Promise<unknown>, hidden: S): () => Owner<S> {
  const q = new Surface<S>(apply);
  let last = 0;
  return () => {
    const me = ++last;
    let closed = false;
    const mine = () => !closed && me === last;
    return {
      set: s => (mine() ? q.set(s) : Promise.resolve(false)),
      close: () => { const p = mine() ? q.set(hidden) : Promise.resolve(false); closed = true; return p; },
    };
  };
}
