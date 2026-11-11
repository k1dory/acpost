// What to show for a request of the app (extension.js), apart from VS Code: so it is tested.
exports.scheme = "acpost-before";

// A request's file: <time>-<random>.json, written whole (renamed into place).
exports.isCommand = name => /^[0-9]+-[0-9a-f]+\.json$/.test(name);

const fresh = 30_000; // a request older than this is not shown (a window opened since)

// plan: the file to show and the text it had before the edit (null: the file alone, no diff), or
// null when the request is not for this window (another folder), too old, or not one.
//
// The text before is the file as it is now with the edit taken back: what the agent wrote, found
// in it, replaced by what was there. A file changed since so that the new text is not in it is
// shown as it is, without a diff.
exports.plan = (cmd, folders, read, now) => {
  if (!cmd || typeof cmd.path !== "string" || !cmd.path || typeof cmd.folder !== "string") return null;
  if (typeof cmd.at !== "number" || now - cmd.at > fresh || cmd.at - now > fresh) return null;
  if (!folders.includes(cmd.folder)) return null;
  const file = cmd.path.startsWith("/") ? cmd.path : cmd.folder.replace(/\/+$/, "") + "/" + cmd.path;
  const text = read(file);
  if (text === null) return null; // gone since (or not a text file to read)
  const title = typeof cmd.title === "string" && cmd.title ? cmd.title : "edit";
  const oldText = typeof cmd.old === "string" ? cmd.old : "", newText = typeof cmd.new === "string" ? cmd.new : "";
  if (cmd.created) return { file, before: "", title };
  if (!newText && !oldText) return { file, before: null, title };
  // The edit taken back, where it is found once: twice, and which one it was is not known.
  if (newText) {
    const i = text.indexOf(newText);
    if (i < 0 || text.indexOf(newText, i + 1) >= 0) return { file, before: null, title };
    return { file, before: text.slice(0, i) + oldText + text.slice(i + newText.length), title };
  }
  return { file, before: null, title }; // text removed: where from is not known
};
