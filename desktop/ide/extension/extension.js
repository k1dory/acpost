// The app's IDE extension: the side panel VS Code opens on the right (its AI chat's place; the
// agents' chat is the app's own, beside the IDE) is closed the first time a folder is opened.
const vscode = require("vscode");

exports.activate = async (ctx) => {
  if (ctx.workspaceState.get("acpost.tidied")) return;
  try { await vscode.commands.executeCommand("workbench.action.closeAuxiliaryBar"); } catch {}
  await ctx.workspaceState.update("acpost.tidied", true);
};
exports.deactivate = () => {};
