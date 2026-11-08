/** A random id (128 bits, hex). crypto.randomUUID needs a secure context, which the app's own
 * scheme may not be; getRandomValues does not. */
export function newId(): string {
  const b = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(b, x => x.toString(16).padStart(2, "0")).join("");
}
