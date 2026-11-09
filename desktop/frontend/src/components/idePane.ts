// The IDE's native pane, driven from the interface: its calls (show, hide, reload) one at a time,
// through this queue only, and the latest wins.
// Each call returns once the native side has queued it (in order, on the main thread), so with
// one in flight the pane ends as the interface last asked; a state asked for while another call
// is out replaces any not sent yet. There is one pane: one queue for it (paneOwner), whichever
// IDE view asks.

export type Rect = { x: number; y: number; w: number; h: number };

export type PaneCalls = {
  show(url: string, r: Rect): Promise<void>;
  hide(): Promise<void>;
  /** Reloads the page when url is the address the pane shows (the native side checks). */
  reload(url: string): Promise<void>;
};

type Want = { url: string; r: Rect | null; done: (applied: boolean) => void; fail: (e: unknown) => void };

export class Pane {
  #calls: PaneCalls;
  #want: Want | null = null; // not sent yet
  #reload: Want | null = null; // a reload not sent yet: after the state asked before or with it
  #busy = false;
  #closed = false;

  constructor(calls: PaneCalls) { this.#calls = calls; }

  /** Shows the pane at r (and opens url, when given): true once done, false when a later state took its place. */
  show(url: string, r: Rect): Promise<boolean> { return this.#ask(url, r); }

  /** Hides the pane; VS Code keeps running. */
  hide(): Promise<boolean> { return this.#ask("", null); }

  /**
   * Reloads the page shown, when it is still url's: in the queue, after the calls asked before it
   * (a later state goes first: the native side then drops a reload for an address no longer shown).
   */
  reload(url: string): Promise<boolean> {
    if (this.#closed || !url) return Promise.resolve(false);
    return new Promise((done, fail) => {
      this.#reload?.done(false);
      this.#reload = { url, r: null, done, fail };
      void this.#pump();
    });
  }

  /** A reload not sent yet, dropped (the view that asked for it is gone). */
  dropReload() {
    this.#reload?.done(false);
    this.#reload = null;
  }

  /** The pane hidden for good: later shows are not sent. */
  close(): Promise<boolean> {
    this.dropReload();
    const p = this.#ask("", null);
    this.#closed = true;
    return p;
  }

  #ask(url: string, r: Rect | null): Promise<boolean> {
    if (this.#closed) return Promise.resolve(false);
    return new Promise((done, fail) => {
      this.#want?.done(false);
      this.#want = { url, r, done, fail };
      void this.#pump();
    });
  }

  async #pump() {
    if (this.#busy) return;
    this.#busy = true;
    while (this.#want || this.#reload) {
      const reload = !this.#want;
      const w = (this.#want ?? this.#reload)!;
      if (reload) this.#reload = null; else this.#want = null;
      try {
        if (reload) await this.#calls.reload(w.url);
        else if (w.r) await this.#calls.show(w.url, w.r);
        else await this.#calls.hide();
        w.done(true);
      } catch (e) { w.fail(e); }
    }
    this.#busy = false;
  }
}

/**
 * The pane's place: the box (window points, f the text zoom), less the strip at its bottom a toast
 * over it takes (the toast, HTML, would be under the native pane); null when too small.
 */
export function paneRect(box: Rect, f: number, toast: Rect | null): Rect | null {
  let h = box.h;
  if (toast && toast.x < box.x + box.w && toast.x + toast.w > box.x && toast.y < box.y + box.h) h = Math.max(0, toast.y - 8 - box.y);
  if (box.w < 4 || h < 4) return null;
  return { x: box.x * f, y: box.y * f, w: box.w * f, h: h * f };
}

export type PaneOwner = {
  show(url: string, r: Rect): Promise<boolean>;
  hide(): Promise<boolean>;
  /** Reloads the page, when it still shows url. */
  reload(url: string): Promise<boolean>;
  /** The owner gone (its view unmounted): it hides the pane unless a newer owner has it; nothing it asks after is sent. */
  close(): Promise<boolean>;
};

/**
 * Owners of one pane, over one queue: the latest owner is the pane's; an older one's calls are
 * dropped (a view leaving with a show still out must not hide the view that came after it).
 */
export function paneOwners(calls: PaneCalls): () => PaneOwner {
  const q = new Pane(calls);
  let last = 0;
  return () => {
    const me = ++last;
    let closed = false;
    const mine = () => !closed && me === last;
    return {
      show: (url, r) => (mine() ? q.show(url, r) : Promise.resolve(false)),
      hide: () => (mine() ? q.hide() : Promise.resolve(false)),
      reload: url => (mine() ? q.reload(url) : Promise.resolve(false)),
      close: () => {
        if (!mine()) { closed = true; return Promise.resolve(false); }
        q.dropReload();
        closed = true;
        return q.hide();
      },
    };
  };
}
