// Calls that overlap (a filter clicked twice, a chat switched while its data loads) answer in any
// order: only the answer to the last one asked is applied.

/** A guard for one kind of call. run(call, apply, fail) applies the answer (or the error) only if no
 * later run was made meanwhile; it resolves true when this run was the last (its loading is over). */
export function latest() {
  let asked = 0;
  return async function run<T>(call: () => Promise<T>, apply: (v: T) => void, fail?: (e: unknown) => void): Promise<boolean> {
    const my = ++asked;
    try {
      const v = await call();
      if (my === asked) apply(v);
    } catch (e) {
      if (my === asked) fail?.(e);
    }
    return my === asked;
  };
}
