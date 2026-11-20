// Props a test changes after mounting, as a parent would (a $state object passed to mount).
export function reactive<T extends object>(v: T): T {
  const s = $state(v);
  return s;
}
