// How a terminal panel attaches to its shell, apart from xterm and Wails so it can be tested.

export type ShellLink = {
  /** Registers what the terminal sends: keys, and its answers to the shell's queries. */
  onData(fn: (s: string) => void): void;
  /** Sends input to the shell (queued until it can go). */
  input(s: string): void;
  /** Starts the shell's output (Node.TermAttach). */
  attach(): Promise<void>;
};

/** Input is wired before the output starts: a shell's first output may ask the terminal
 *  something (ESC[6n, where is the cursor), and the terminal's answer goes back as input —
 *  lost when it came before the attach call returned, and the shell waited for it. */
export async function attachShell(l: ShellLink): Promise<void> {
  l.onData(l.input);
  await l.attach();
}
