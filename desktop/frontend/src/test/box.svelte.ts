// A value in Svelte state, for a test to bind a component's prop to (imported after the modules
// are reset, with the component: one runtime for both).
export function box<T>(v: T) {
  let s = $state(v);
  return { get v() { return s; }, set v(x: T) { s = x; } };
}
