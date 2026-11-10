// The composer's text once a message went: what was sent goes, what was typed meanwhile stays.

/** The text left in the composer after `sent` went, the composer now holding `now`. */
export function afterSent(now: string, sent: string): string {
  if (sent === "") return now; // files alone went: no text of theirs to take out
  if (now === sent) return "";
  // Typed on after it while it went: that stays.
  if (now.startsWith(sent)) return now.slice(sent.length).replace(/^\s+/, "");
  return now; // changed meanwhile: the person's, kept as it is
}

/** Why a file cannot be attached (the node's limits: how many, how big together), or "". */
export function overLimits(
  have: { size: number }[], add: { size: number }, limits: { max_files: number; max_files_size: number } | null,
): "" | "count" | "size" {
  if (!limits) return "";
  if (limits.max_files > 0 && have.length + 1 > limits.max_files) return "count";
  const total = have.reduce((a, f) => a + f.size, 0) + add.size;
  if (limits.max_files_size > 0 && total > limits.max_files_size) return "size";
  return "";
}
