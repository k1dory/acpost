export * as Node from "../../bindings/github.com/k1dory/acpost/desktop/node.js";
import { Events } from "@wailsio/runtime";

export function on(name: string, fn: (data: any) => void): () => void {
  return Events.On(name, (e: any) => fn(e.data));
}

export function b64(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let bin = "";
  for (const x of bytes) bin += String.fromCharCode(x);
  return btoa(bin);
}

export function unb64(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export { errInfo, errText, type ErrInfo } from "./errors";
