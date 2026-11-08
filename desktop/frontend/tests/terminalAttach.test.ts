// node --test tests/ (Node 22.18+ runs TypeScript as it is)
import { test } from "node:test";
import assert from "node:assert/strict";
import { attachShell } from "../src/components/terminalAttach.ts";

test("the terminal's answer to an early query reaches the shell", async () => {
  const sent: string[] = [];
  let terminal: ((s: string) => void) | null = null;
  let resolveAttach!: () => void;
  const done = attachShell({
    onData(fn) { terminal = fn; },
    input(s) { sent.push(s); },
    attach() {
      // The shell's first output asks where the cursor is; xterm answers at once, while the
      // attach call has not returned yet.
      terminal?.("\x1b[1;1R");
      return new Promise<void>(r => { resolveAttach = r; });
    },
  });
  assert.deepEqual(sent, ["\x1b[1;1R"]);
  resolveAttach();
  await done;
});
