// A node for tests: every binding a vi.fn (a test sets what it answers), and the Wails events the
// app listens to, emitted by hand.
import { vi } from "vitest";

export type Fake = {
  Node: Record<string, ReturnType<typeof vi.fn>>;
  on: (name: string, fn: (data: any) => void) => () => void;
  emit: (name: string, data: any) => void;
  listeners: (name: string) => number;
};

export function fakeNode(): Fake {
  const fns: Record<string, ReturnType<typeof vi.fn>> = {};
  // A binding no test set up answers nothing (as a void call does).
  const Node = new Proxy(fns, { get: (o, k: string) => (o[k] ??= vi.fn(async () => undefined)) });
  const subs = new Map<string, Set<(data: any) => void>>();
  return {
    Node,
    on(name, fn) {
      const s = subs.get(name) ?? new Set();
      s.add(fn);
      subs.set(name, s);
      return () => s.delete(fn);
    },
    emit(name, data) { for (const fn of [...(subs.get(name) ?? [])]) fn(data); },
    listeners: name => subs.get(name)?.size ?? 0,
  };
}

/** A promise and what settles it, for answers that come when the test says. */
export function deferred<T = any>() {
  let resolve!: (v: T) => void, reject!: (e: any) => void;
  const promise = new Promise<T>((a, b) => { resolve = a; reject = b; });
  return { promise, resolve, reject };
}

/** Lets pending promise callbacks run. */
export const flush = () => new Promise(r => setTimeout(r, 0));
