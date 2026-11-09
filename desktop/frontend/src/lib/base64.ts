// Base64 for what goes to and comes from the node (files, artifacts): one way to do it.

/** Text as base64 (its UTF-8). */
export function b64(s: string): string {
  return b64bytes(new TextEncoder().encode(s));
}

/** Bytes as base64, a chunk at a time (a file of megabytes in one call overflows the stack). */
export function b64bytes(b: Uint8Array): string {
  let bin = "";
  for (let i = 0; i < b.length; i += 0x8000) bin += String.fromCharCode(...b.subarray(i, i + 0x8000));
  return btoa(bin);
}

export function unb64(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
