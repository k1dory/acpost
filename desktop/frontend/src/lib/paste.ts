// What a paste into the composer attaches. The textarea itself takes only text: an image on the
// clipboard (a screenshot, an image copied in Preview or a browser) or a file copied in Finder
// would be dropped without this.
//
// What the clipboard holds, and what happens:
//   text only                      → the text is pasted as usual (nothing here)
//   files only (a screenshot, an image copied alone)
//                                  → the files are attached, the default paste prevented
//   files + text that is only their names or file:// addresses (Finder puts both)
//                                  → the files are attached, the names not pasted
//   files + other text (a part of a web page or a document, copied with its pictures)
//                                  → the text is pasted, the pictures are not attached: one
//                                    copy gives one thing, never both
// Ctrl+V is not paste in macOS's WebKit and gets no binding here: ⌘V is the paste.

/** What a paste event carries, as the parts used (a DataTransfer, or what a test gives). */
export type Clip = {
  files?: ArrayLike<File> | null;
  items?: ArrayLike<{ kind: string; type: string; getAsFile(): File | null }> | null;
  getData(type: string): string;
};

/** The files of a clipboard: its file list whole (two files of one name, size and date may still
 * differ: nothing is dropped by what they say of themselves); its items of kind "file" only when
 * the list is empty (WebKit puts a pasted image in one or the other, or both). */
export function clipFiles(c: Clip): File[] {
  const list = Array.from(c.files ?? []).filter((f): f is File => !!f);
  if (list.length) return list;
  const out: File[] = [];
  for (const it of Array.from(c.items ?? [])) {
    if (it.kind !== "file") continue;
    const f = it.getAsFile();
    if (f) out.push(f);
  }
  return out;
}

/** Whether a line of the clipboard's text is one of the files: its exact name, or a file: URL of it. */
function namesFile(line: string, names: Set<string>): boolean {
  if (names.has(line)) return true;
  if (!/^file:/i.test(line)) return false;
  try {
    const u = new URL(line);
    if (u.protocol !== "file:") return false;
    return names.has(decodeURIComponent(u.pathname.split("/").pop() ?? ""));
  } catch { return false; }
}

/** The files a paste attaches ([]: let the text paste as usual). */
export function pasteFiles(c: Clip): File[] {
  const files = clipFiles(c);
  if (!files.length) return [];
  const text = c.getData("text/plain") || "";
  if (text === "") return files;
  // Finder: the text is exactly the files' names (or their file: URLs), one per line, and nothing
  // else. Compared as it is: no trimming, no empty line (a name may begin with a space; text with
  // more than the names is text). One line ending at the very end (\n or \r\n) is allowed: Finder
  // may end the list with one.
  const names = new Set(files.map(f => f.name));
  const lines = text.replace(/\r?\n$/, "").split(/\r\n|\n/);
  return lines.every(l => l !== "" && namesFile(l, names)) ? files : [];
}

const exts: Record<string, string> = {
  "image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp", "image/heic": "heic",
  "image/tiff": "tiff", "image/bmp": "bmp", "image/svg+xml": "svg", "application/pdf": "pdf", "text/plain": "txt",
};
/** Names WebKit gives a pasted picture, which say nothing of it (only for images: a file named
 * image.png that is not one keeps its name; a dotfile like .env is a real name). */
const generic = /^(image|blob|untitled)\.[a-z0-9]+$/i;
let pasted = 0;

/** A name not among `taken`: the name itself, or "name (2).ext", "name (3).ext"… (the node refuses
 * two files of one name in a message). */
export function freeName(name: string, taken: Iterable<string>): string {
  const used = new Set(taken);
  if (!used.has(name)) return name;
  const dot = name.lastIndexOf(".");
  const [base, ext] = dot > 0 ? [name.slice(0, dot), name.slice(dot)] : [name, ""];
  let i = 2;
  while (used.has(`${base} (${i})${ext}`)) i++;
  return `${base} (${i})${ext}`;
}

/** The name a pasted file is attached under, `taken` being the names attached when it is (asked
 * in the draft's queue, so earlier pastes and drops still being read are among them): a file with
 * no name, or a picture with WebKit's generic one, becomes pasted-HHMMSS-n.ext (by its type, n
 * counting the session's pastes); a real name stays, unless taken. */
export function pasteName(f: { name: string; type: string }, taken: Iterable<string>, now = new Date()): string {
  if (f.name && !(f.type.startsWith("image/") && generic.test(f.name))) return freeName(f.name, taken);
  const hms = [now.getHours(), now.getMinutes(), now.getSeconds()].map(n => String(n).padStart(2, "0")).join("");
  const ext = exts[f.type] ?? (f.name.includes(".") ? f.name.split(".").pop()! : "bin");
  return freeName(`pasted-${hms}-${++pasted}.${ext}`, taken);
}
