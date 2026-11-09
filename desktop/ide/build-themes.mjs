// Builds the IDE's colour themes (acpost Light, acpost Dark) from the app's own tokens
// (frontend/src/app.css): VS Code wears the app's colours, and follows them when they change.
import { readFileSync, writeFileSync } from "node:fs";

const css = readFileSync("../frontend/src/app.css", "utf8");
function tokens(selector) {
  const i = css.indexOf(selector + " {");
  if (i < 0) throw new Error("no " + selector + " in app.css");
  const body = css.slice(i, css.indexOf("}", i));
  const out = {};
  for (const m of body.matchAll(/--([a-z0-9-]+):\s*(#[0-9a-fA-F]{3,8})\b/g)) out[m[1]] = m[2].toLowerCase();
  return out;
}
const light = tokens(":root"), dark = { ...light, ...tokens(':root[data-theme="dark"]') };
// A token the themes name that app.css does not have is an error, not "undefined" in a colour.
const strict = (o, name) => new Proxy(o, {
  get(t, k) {
    if (typeof k === "string" && !(k in t)) throw new Error("no --" + k + " in app.css (" + name + ")");
    return t[k];
  },
});
const hl = {
  light: { kw: "#a626a4", str: "#50a14f", num: "#b76b01", fn: "#4078f2", var: "#c45a2c", type: "#c18401" },
  dark: { kw: "#c792ea", str: "#a5d6a7", num: "#f7c37a", fn: "#82aaff", var: "#f2a37a", type: "#ffcb6b" },
};

function colors(p) {
  return {
    foreground: p["ink-2"], descriptionForeground: p.muted, focusBorder: p.accent + "80", "widget.border": p.line,
    "widget.shadow": "#00000022", "icon.foreground": p["ink-2"], "textLink.foreground": p.accent, "textLink.activeForeground": p.accent,
    "selection.background": p.accent + "40", "sash.hoverBorder": p.accent + "80",
    "editor.background": p.page, "editor.foreground": p.ink, "editorLineNumber.foreground": p.muted + "99",
    "editorLineNumber.activeForeground": p["ink-2"], "editor.lineHighlightBackground": p.hover, "editor.lineHighlightBorder": p.hover,
    "editor.selectionBackground": p.accent + "33", "editor.inactiveSelectionBackground": p.accent + "1f",
    "editor.findMatchBackground": p.warn + "55", "editor.findMatchHighlightBackground": p.warn + "2a",
    "editor.wordHighlightBackground": p.sel, "editorCursor.foreground": p.ink, "editorWhitespace.foreground": p.line,
    "editorIndentGuide.background1": p["line-soft"], "editorIndentGuide.activeBackground1": p.line,
    "editorBracketMatch.background": p.sel, "editorBracketMatch.border": p.line,
    "editorWidget.background": p.card, "editorWidget.border": p.line, "editorSuggestWidget.background": p.card,
    "editorSuggestWidget.border": p.line, "editorSuggestWidget.selectedBackground": p.sel, "editorHoverWidget.background": p.card,
    "editorHoverWidget.border": p.line, "editorGutter.background": p.page, "editorGutter.addedBackground": p.good,
    "editorGutter.modifiedBackground": p.accent, "editorGutter.deletedBackground": p.crit,
    "editorError.foreground": p.crit, "editorWarning.foreground": p.warn, "editorInfo.foreground": p.accent,
    "diffEditor.insertedTextBackground": p.good + "26", "diffEditor.removedTextBackground": p.crit + "26",
    "diffEditor.insertedLineBackground": p.add, "diffEditor.removedLineBackground": p.del,
    "editorGroup.border": p.line, "editorGroupHeader.tabsBackground": p.side, "editorGroupHeader.tabsBorder": p["line-soft"],
    "editorGroupHeader.noTabsBackground": p.side, "breadcrumb.foreground": p.muted, "breadcrumb.focusForeground": p.ink,
    "breadcrumb.background": p.page,
    "tab.activeBackground": p.page, "tab.inactiveBackground": p.side, "tab.border": p.side, "tab.activeBorderTop": p.page,
    "tab.activeBorder": p.page, "tab.activeForeground": p.ink, "tab.inactiveForeground": p.muted, "tab.hoverBackground": p.hover,
    "tab.unfocusedActiveForeground": p["ink-2"], "tab.unfocusedInactiveForeground": p.muted,
    "activityBar.background": p.side, "activityBar.foreground": p.ink, "activityBar.inactiveForeground": p.muted,
    "activityBar.border": p.line, "activityBar.activeBorder": p.ink, "activityBarTop.foreground": p.ink,
    "activityBarTop.inactiveForeground": p.muted, "activityBarTop.activeBorder": p.ink, "activityBarTop.background": p.side,
    "activityBarBadge.background": p.accent, "activityBarBadge.foreground": "#ffffff",
    "sideBar.background": p.side, "sideBar.foreground": p["ink-2"], "sideBar.border": p.line, "sideBarTitle.foreground": p.ink,
    "sideBarSectionHeader.background": p.side, "sideBarSectionHeader.foreground": p.ink, "sideBarSectionHeader.border": p.side,
    "titleBar.activeBackground": p.side, "titleBar.inactiveBackground": p.side, "titleBar.activeForeground": p["ink-2"],
    "titleBar.inactiveForeground": p.muted, "titleBar.border": p.line,
    "statusBar.background": p.side, "statusBar.foreground": p.muted, "statusBar.border": p.line,
    "statusBar.noFolderBackground": p.side, "statusBar.debuggingBackground": p.side, "statusBar.debuggingForeground": p.warn,
    "statusBarItem.remoteBackground": p.side, "statusBarItem.remoteForeground": p.muted, "statusBarItem.hoverBackground": p.hover,
    "panel.background": p.page, "panel.border": p.line, "panelTitle.activeForeground": p.ink, "panelTitle.activeBorder": p.ink,
    "panelTitle.inactiveForeground": p.muted, "panelSectionHeader.background": p.side,
    "terminal.background": p.page, "terminal.foreground": p.ink, "terminalCursor.foreground": p.ink,
    "terminal.selectionBackground": p.accent + "40",
    "list.activeSelectionBackground": p.sel, "list.activeSelectionForeground": p.ink, "list.inactiveSelectionBackground": p.sel,
    "list.inactiveSelectionForeground": p.ink, "list.hoverBackground": p.hover, "list.hoverForeground": p.ink,
    "list.focusOutline": "#00000000", "list.focusBackground": p.sel, "list.highlightForeground": p.accent,
    "list.errorForeground": p.crit, "list.warningForeground": p.warn, "list.dropBackground": p["accent-bg"],
    "tree.indentGuidesStroke": p.line,
    "input.background": p.card, "input.border": p.line, "input.foreground": p.ink, "input.placeholderForeground": p.muted,
    "inputOption.activeBackground": p["accent-bg"], "inputOption.activeBorder": p.accent + "80",
    "dropdown.background": p.card, "dropdown.border": p.line, "dropdown.listBackground": p.card,
    "checkbox.background": p.card, "checkbox.border": p.line,
    "quickInput.background": p.card, "quickInputList.focusBackground": p.sel, "quickInputList.focusForeground": p.ink,
    "pickerGroup.border": p["line-soft"], "pickerGroup.foreground": p.muted,
    "menu.background": p.card, "menu.foreground": p["ink-2"], "menu.border": p.line, "menu.selectionBackground": p.sel,
    "menu.selectionForeground": p.ink, "menu.separatorBackground": p["line-soft"],
    "button.background": p.btn, "button.foreground": p["btn-ink"], "button.hoverBackground": p.btn + "dd", "button.border": p.btn,
    "button.secondaryBackground": p.sel, "button.secondaryForeground": p.ink, "button.secondaryHoverBackground": p.hover,
    "badge.background": p.sel, "badge.foreground": p.ink, "progressBar.background": p.accent,
    "scrollbar.shadow": "#00000000", "scrollbarSlider.background": p.muted + "33", "scrollbarSlider.hoverBackground": p.muted + "55",
    "scrollbarSlider.activeBackground": p.muted + "77",
    "notifications.background": p.card, "notifications.border": p.line, "notificationCenterHeader.background": p.card,
    "notificationToast.border": p.line, "notificationsErrorIcon.foreground": p.crit, "notificationsWarningIcon.foreground": p.warn,
    "notificationsInfoIcon.foreground": p.accent,
    "gitDecoration.modifiedResourceForeground": p.warn, "gitDecoration.untrackedResourceForeground": p.good,
    "gitDecoration.addedResourceForeground": p.good, "gitDecoration.deletedResourceForeground": p.crit,
    "gitDecoration.ignoredResourceForeground": p.muted, "gitDecoration.conflictingResourceForeground": p.crit,
    "welcomePage.background": p.page, "walkThrough.embeddedEditorBackground": p.card,
    "settings.headerForeground": p.ink, "settings.modifiedItemIndicator": p.accent, "keybindingLabel.background": p.sel,
    "keybindingLabel.border": p.line, "keybindingLabel.bottomBorder": p.line, "keybindingLabel.foreground": p["ink-2"],
    "extensionButton.prominentBackground": p.btn, "extensionButton.prominentForeground": p["btn-ink"],
    "extensionButton.background": p.btn, "extensionButton.foreground": p["btn-ink"],
  };
}
function tokenColors(h, p) {
  const r = (scope, foreground, fontStyle) => ({ scope, settings: fontStyle ? { foreground, fontStyle } : { foreground } });
  return [
    r(["comment", "punctuation.definition.comment"], p.muted, "italic"),
    r(["keyword", "storage", "storage.type", "keyword.control", "keyword.operator.new", "keyword.operator.expression"], h.kw),
    r(["string", "string.quoted", "string.template", "punctuation.definition.string"], h.str),
    r(["constant.numeric", "constant.language", "constant.character", "support.constant"], h.num),
    r(["entity.name.function", "support.function", "meta.function-call entity.name.function"], h.fn),
    r(["entity.name.type", "entity.name.class", "support.type", "support.class", "entity.other.inherited-class"], h.type),
    r(["variable.parameter", "variable.other.property", "meta.object-literal.key", "support.type.property-name"], h.var),
    r(["entity.name.tag", "markup.heading"], h.kw),
    r(["entity.other.attribute-name"], h.var),
    r(["markup.bold"], p.ink, "bold"), r(["markup.italic"], p.ink, "italic"),
    r(["markup.inserted"], p.good), r(["markup.deleted"], p.crit), r(["markup.changed"], p.warn),
    r(["invalid"], p.crit),
  ];
}
function theme(name, type, p, h) {
  return { name, type, semanticHighlighting: true, colors: colors(p), tokenColors: tokenColors(h, p),
    semanticTokenColors: { function: h.fn, method: h.fn, type: h.type, class: h.type, interface: h.type, parameter: h.var, property: h.var, keyword: h.kw, string: h.str, number: h.num } };
}
writeFileSync("extension/themes/acpost-light.json", JSON.stringify(theme("acpost Light", "light", strict(light, "light"), hl.light), null, 2) + "\n");
writeFileSync("extension/themes/acpost-dark.json", JSON.stringify(theme("acpost Dark", "dark", strict(dark, "dark"), hl.dark), null, 2) + "\n");
console.log("themes from", Object.keys(light).length, "tokens");
