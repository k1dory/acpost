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

/** The files of a clipboard: its files and its items of kind "file" (WebKit puts a pasted image
 * in one or the other, or both: the same file once). */
export function clipFiles(c: Clip): File[] {
  const out: File[] = [];
  const seen = (f: File) => out.some(x => x === f || (x.name === f.name && x.size === f.size && x.type === f.type && x.lastModified === f.lastModified));
  for (const f of Array.from(c.files ?? [])) if (f && !seen(f)) out.push(f);
  for (const it of Array.from(c.items ?? [])) {
    if (it.kind !== "file") continue;
    const f = it.getAsFile();
    if (f && !seen(f)) out.push(f);
  }
  return out;
}

/** The files a paste attaches ([]: let the text paste as usual). */
export function pasteFiles(c: Clip): File[] {
  const files = clipFiles(c);
  if (!files.length) return [];
  const text = (c.getData("text/plain") || "").trim();
  if (!text) return files;
  // Finder: the text is the files' names (or their file:// addresses), one per line.
  const lines = text.split(/\r?\n/).map(l => l.trim()).filter(Boolean);
  const names = new Set(files.map(f => f.name));
  const isName = (l: string) => names.has(l) || names.has(decodeURIComponent(l.replace(/^file:\/\//, "")).split("/").pop() ?? "");
  return lines.every(isName) ? files : [];
}

const exts: Record<string, string> = {
  "image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp", "image/heic": "heic",
  "image/tiff": "tiff", "image/bmp": "bmp", "image/svg+xml": "svg", "application/pdf": "pdf", "text/plain": "txt",
};
/** Names a pasted image gets from WebKit, which say nothing: replaced. */
const generic = /^(image|blob|untitled)?(\.[a-z0-9]+)?$/i;
let pasted = 0;

/** Pasted files with names of their own: a pasted image (no name, or "image.png") becomes
 * pasted-HHMMSS-n.ext (by its type); a real name stays, unless one already attached has it (the
 * node refuses two files of one name in a message): then "name (2).ext". `taken`: the names
 * attached already. */
export function pasteNames(files: File[], taken: Iterable<string>, now = new Date()): File[] {
  const used = new Set(taken);
  const hms = [now.getHours(), now.getMinutes(), now.getSeconds()].map(n => String(n).padStart(2, "0")).join("");
  return files.map(f => {
    let name = f.name;
    if (generic.test(name)) {
      const ext = exts[f.type] ?? (name.includes(".") ? name.split(".").pop()! : "bin");
      do name = `pasted-${hms}-${++pasted}.${ext}`; while (used.has(name));
    } else if (used.has(name)) {
      const dot = name.lastIndexOf(".");
      const [base, ext] = dot > 0 ? [name.slice(0, dot), name.slice(dot)] : [name, ""];
      let i = 2;
      while (used.has(`${base} (${i})${ext}`)) i++;
      name = `${base} (${i})${ext}`;
    }
    used.add(name);
    return name === f.name ? f : new File([f], name, { type: f.type, lastModified: f.lastModified });
  });
}
