// The IDE's native pane, driven from the interface: its calls one at a time and the latest wins.
// Each call returns once the native side has queued it (in order, on the main thread), so with
// one in flight the pane ends as the interface last asked; a state asked for while another call
// is out replaces any not sent yet; after close nothing is shown again.

export type Rect = { x: number; y: number; w: number; h: number };

export type PaneCalls = {
  show(url: string, r: Rect): Promise<void>;
  hide(): Promise<void>;
};

type Want = { url: string; r: Rect | null; done: (applied: boolean) => void; fail: (e: unknown) => void };

export class Pane {
  #calls: PaneCalls;
  #want: Want | null = null; // not sent yet
  #busy = false;
  #closed = false;

  constructor(calls: PaneCalls) { this.#calls = calls; }

  /** Shows the pane at r (and opens url, when given): true once done, false when a later state took its place. */
  show(url: string, r: Rect): Promise<boolean> { return this.#ask(url, r); }

  /** Hides the pane; VS Code keeps running. */
  hide(): Promise<boolean> { return this.#ask("", null); }

  /** The pane hidden for good: later shows are not sent. */
  close(): Promise<boolean> {
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
    while (this.#want) {
      const w = this.#want;
      this.#want = null;
      try {
        if (w.r) await this.#calls.show(w.url, w.r);
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
