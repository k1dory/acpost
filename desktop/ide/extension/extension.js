// The app's IDE extension: the side panel VS Code opens on the right (its AI chat's place; the
// agents' chat is the app's own, beside the IDE) is closed the first time a folder is opened, and
// what the app asks to be shown (a file an agent edited, as a diff) is shown.
const vscode = require("vscode");
const fs = require("fs");
const path = require("path");
const reveal = require("./reveal");

exports.activate = async (ctx) => {
  try { watch(ctx); } catch {} // the IDE works without it
  if (ctx.workspaceState.get("acpost.tidied")) return;
  try { await vscode.commands.executeCommand("workbench.action.closeAuxiliaryBar"); } catch { return; } // tried again next time
  await ctx.workspaceState.update("acpost.tidied", true);
};
exports.deactivate = () => {};

// The app's requests: files it writes into its own folder (commands, beside the server's; the
// folder is the user's alone), each read once by the window that has its folder open. No port and
// nothing to connect to: who may write there may write the user's files anyway.
function watch(ctx) {
  const dir = path.resolve(ctx.extensionPath, "..", "..", "..", "commands");
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const before = new Map(); // what a diff's left side shows, by its address
  const changed = new vscode.EventEmitter();
  ctx.subscriptions.push(changed, vscode.workspace.registerTextDocumentContentProvider(reveal.scheme, {
    onDidChange: changed.event,
    provideTextDocumentContent: uri => before.get(uri.toString()) ?? "",
  }));
  const seen = new Set();
  const take = name => {
    if (!reveal.isCommand(name) || seen.has(name)) return;
    seen.add(name);
    if (seen.size > 500) seen.clear();
    let cmd;
    try { cmd = JSON.parse(fs.readFileSync(path.join(dir, name), "utf8")); } catch { return; }
    const folders = (vscode.workspace.workspaceFolders ?? []).map(f => f.uri.fsPath);
    const plan = reveal.plan(cmd, folders, file => { try { return fs.readFileSync(file, "utf8"); } catch { return null; } }, Date.now());
    if (!plan) return;
    show(plan).catch(() => {});
  };
  async function show(plan) {
    const file = vscode.Uri.file(plan.file);
    const quiet = { preview: true, preserveFocus: true };
    if (plan.before === null) {
      await vscode.window.showTextDocument(file, quiet);
      return;
    }
    const left = vscode.Uri.from({ scheme: reveal.scheme, path: plan.file });
    before.set(left.toString(), plan.before);
    if (before.size > 50) before.delete(before.keys().next().value);
    changed.fire(left); // the same file edited again: its left side anew
    await vscode.commands.executeCommand("vscode.diff", left, file, path.basename(plan.file) + " (" + plan.title + ")", quiet);
  }
  const w = fs.watch(dir, (_, name) => { if (name) setTimeout(() => take(String(name)), 20); });
  ctx.subscriptions.push({ dispose: () => w.close() });
}
