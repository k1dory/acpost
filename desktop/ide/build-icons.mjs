// Builds the IDE's product icon theme: VS Code's icons (codicons) replaced by Lucide's (line
// icons, the app's style), a font of only those glyphs. Run when the map changes: `npm run icons`.
import { readFileSync, writeFileSync } from "node:fs";
import subsetFont from "subset-font";

// codicon id → lucide name
const map = {
  files: "files", search: "search", "source-control": "git-branch", "debug-alt": "bug", "debug-alt-small": "bug",
  extensions: "blocks", "settings-gear": "settings", gear: "settings", account: "circle-user", "remote-explorer": "monitor",
  close: "x", "chevron-down": "chevron-down", "chevron-right": "chevron-right", "chevron-left": "chevron-left", "chevron-up": "chevron-up",
  folder: "folder", "folder-opened": "folder-open", "root-folder": "folder", "root-folder-opened": "folder-open", file: "file",
  "new-file": "file-plus", "new-folder": "folder-plus", refresh: "refresh-cw", sync: "refresh-cw", "collapse-all": "chevrons-down-up",
  "expand-all": "chevrons-up-down", ellipsis: "ellipsis", more: "ellipsis", "kebab-vertical": "ellipsis-vertical",
  "split-horizontal": "columns-2", "split-vertical": "rows-2", terminal: "square-terminal", "terminal-bash": "square-terminal",
  trash: "trash-2", add: "plus", plus: "plus", remove: "minus", check: "check", "check-all": "check-check", pass: "circle-check",
  "pass-filled": "circle-check", error: "circle-x", warning: "triangle-alert", info: "info", bell: "bell", "bell-dot": "bell-dot",
  filter: "funnel", "filter-filled": "funnel", "clear-all": "list-x", menu: "menu", "layout-sidebar-left": "panel-left",
  "layout-sidebar-left-off": "panel-left", "layout-panel": "panel-bottom", "layout-panel-off": "panel-bottom",
  "layout-sidebar-right": "panel-right", "layout-sidebar-right-off": "panel-right", "go-to-file": "file-search",
  pin: "pin", pinned: "pin", copy: "copy", edit: "pencil", save: "save", "save-all": "save-all", history: "history",
  "git-commit": "git-commit-horizontal", "git-pull-request": "git-pull-request", "git-merge": "git-merge", "git-compare": "git-compare",
  cloud: "cloud", "cloud-download": "cloud-download", "cloud-upload": "cloud-upload", home: "house", play: "play", run: "play",
  "debug-start": "play", "debug-continue": "play", "debug-stop": "square", "debug-pause": "pause", "debug-restart": "rotate-ccw",
  "debug-step-over": "redo", "debug-step-into": "arrow-down-to-line", "debug-step-out": "arrow-up-from-line",
  "debug-disconnect": "unplug", "link-external": "external-link", link: "link", lock: "lock", unlock: "lock-open",
  eye: "eye", "eye-closed": "eye-off", preview: "eye", "open-preview": "eye", "list-tree": "list-tree", "list-flat": "list",
  "list-selection": "list-checks", "arrow-up": "arrow-up", "arrow-down": "arrow-down", "arrow-left": "arrow-left",
  "arrow-right": "arrow-right", "arrow-swap": "arrow-left-right", replace: "replace", "replace-all": "replace-all",
  "case-sensitive": "case-sensitive", "whole-word": "whole-word", regex: "regex", discard: "undo-2", undo: "undo-2", redo: "redo-2",
  diff: "diff", server: "server", "light-bulb": "lightbulb", sparkle: "sparkles", comment: "message-square",
  "comment-discussion": "messages-square", bookmark: "bookmark", tag: "tag", globe: "globe", zap: "zap",
  "screen-full": "maximize-2", "screen-normal": "minimize-2", "chrome-maximize": "maximize", "chrome-restore": "minimize",
  "chrome-close": "x", "chrome-minimize": "minus", "panel-maximize": "maximize-2", "panel-restore": "minimize-2",
  "panel-close": "x", issues: "circle-dot", output: "scroll-text", notebook: "notebook", book: "book", question: "circle-help",
  "word-wrap": "wrap-text", "circle-large-outline": "circle", "circle-outline": "circle", "circle-slash": "ban",
  "cloud-download-alt": "download", desktop: "monitor", "device-desktop": "monitor", "symbol-file": "file",
  "go-to-search": "search", "search-fuzzy": "search", "search-stop": "circle-stop", "stop-circle": "circle-stop",
  "add-small": "plus", "close-all": "x", "split-down": "rows-2", "layers": "layers", "layout": "layout-dashboard",
  "settings": "sliders-horizontal", "tools": "wrench", "beaker": "flask-conical", "testing-run-icon": "play",
  "graph": "chart-line", "pulse": "activity", "watch": "clock", "clock": "clock", "calendar": "calendar",
  "mail": "mail", "person": "user", "organization": "users", "key": "key-round", "shield": "shield", "verified": "badge-check",
  "unverified": "badge-x", "star": "star", "star-full": "star", "star-empty": "star", "heart": "heart",
  "thumbsup": "thumbs-up", "thumbsdown": "thumbs-down", "send": "send", "attach": "paperclip", "mention": "at-sign",
  "code": "code", "json": "braces", "markdown": "file-text", "copilot": "sparkles",
};

const cp = JSON.parse(readFileSync("node_modules/lucide-static/font/codepoints.json", "utf8"));
const defs = {};
const used = new Set();
const missing = [];
for (const [codicon, lucide] of Object.entries(map)) {
  const c = cp[lucide];
  if (!c) { missing.push(`${codicon} → ${lucide}`); continue; }
  defs[codicon] = { fontCharacter: "\\" + c.toString(16).toUpperCase() };
  used.add(c);
}
if (missing.length) { console.error("not in Lucide:\n  " + missing.join("\n  ")); process.exit(1); }

const font = readFileSync("node_modules/lucide-static/font/lucide.ttf");
const sub = await subsetFont(font, String.fromCodePoint(...used), { targetFormat: "woff2" });
writeFileSync("extension/icons/acpost.woff2", sub);
writeFileSync("extension/icons/acpost-product-icons.json", JSON.stringify({
  fonts: [{ id: "acpost-icons", src: [{ path: "./acpost.woff2", format: "woff2" }], weight: "normal", style: "normal" }],
  iconDefinitions: defs,
}, null, 2) + "\n");
console.log(`${Object.keys(defs).length} icons, ${used.size} glyphs, ${sub.length} bytes`);
