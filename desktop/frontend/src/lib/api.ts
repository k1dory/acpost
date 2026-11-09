export * as Node from "../../bindings/github.com/k1dory/acpost/desktop/node.js";
import { Events } from "@wailsio/runtime";

export function on(name: string, fn: (data: any) => void): () => void {
  return Events.On(name, (e: any) => fn(e.data));
}

export { b64, b64bytes, unb64 } from "./base64";
export { errInfo, errText, type ErrInfo } from "./errors";
