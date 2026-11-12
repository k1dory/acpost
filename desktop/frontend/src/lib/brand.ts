// Which coding agent is behind an agent: the CLI the node runs over ACP (what it said it is, else its
// command's name), else the agent's own name. One place, so the sidebar, the messages, the composer
// and the board draw the same mark for it.

export type BrandId =
  | "claude" | "codex" | "gemini" | "copilot" | "opencode" | "cursor" | "amp" | "devin" | "kiro" | "goose"
  | "qwen" | "kimi" | "deepseek" | "cline" | "aider" | "auggie" | "codebuddy" | "zed";

export type Brand = {
  id: BrandId;
  title: string; // the product's name, as a tooltip says it
  short: string; // as a list says it beside a chat
  mark: boolean; // a drawn mark; false: its initials on a tile in its hue
  tile: string;
  hue: number;
  sat: number; // the tile's saturation: low for a brand that is black and white
};

// In order of matching: a name can carry two (@zed-industries/claude-code-acp is Claude Code, run by
// an adapter Zed wrote), so the agents come before Zed, and the longer names before the shorter.
const brands: (Brand & { re: RegExp })[] = [
  { id: "claude", title: "Claude Code", short: "Claude", mark: true, tile: "C", hue: 15, sat: 60, re: /claude|anthropic/ },
  { id: "codebuddy", title: "CodeBuddy", short: "CodeBuddy", mark: false, tile: "CB", hue: 245, sat: 60, re: /codebuddy/ },
  { id: "opencode", title: "OpenCode", short: "OpenCode", mark: false, tile: "OC", hue: 220, sat: 8, re: /opencode/ },
  { id: "codex", title: "Codex", short: "Codex", mark: true, tile: "AI", hue: 160, sat: 10, re: /codex|openai|(?:^|[^a-z])gpt/ },
  { id: "gemini", title: "Gemini CLI", short: "Gemini", mark: true, tile: "G", hue: 220, sat: 70, re: /gemini/ },
  { id: "copilot", title: "GitHub Copilot", short: "Copilot", mark: true, tile: "GH", hue: 260, sat: 15, re: /copilot/ },
  { id: "cursor", title: "Cursor", short: "Cursor", mark: true, tile: "Cu", hue: 220, sat: 8, re: /cursor/ },
  { id: "auggie", title: "Auggie (Augment Code)", short: "Auggie", mark: false, tile: "Au", hue: 150, sat: 45, re: /auggie|augment/ },
  { id: "qwen", title: "Qwen Code", short: "Qwen", mark: false, tile: "Q", hue: 255, sat: 60, re: /qwen/ },
  { id: "kimi", title: "Kimi CLI", short: "Kimi", mark: false, tile: "K", hue: 210, sat: 25, re: /kimi|moonshot/ },
  { id: "deepseek", title: "DeepSeek", short: "DeepSeek", mark: false, tile: "DS", hue: 228, sat: 70, re: /deepseek/ },
  { id: "goose", title: "Goose", short: "Goose", mark: false, tile: "G", hue: 30, sat: 10, re: /goose/ },
  { id: "kiro", title: "Kiro", short: "Kiro", mark: false, tile: "K", hue: 270, sat: 65, re: /kiro/ },
  { id: "devin", title: "Devin", short: "Devin", mark: false, tile: "D", hue: 170, sat: 50, re: /(?:^|[^a-z])devin(?![a-z])/ },
  { id: "cline", title: "Cline", short: "Cline", mark: false, tile: "Cl", hue: 200, sat: 12, re: /(?:^|[^a-z])cline(?![a-z])/ },
  { id: "aider", title: "Aider", short: "Aider", mark: false, tile: "Ai", hue: 135, sat: 50, re: /(?:^|[^a-z])aider/ },
  { id: "amp", title: "Amp", short: "Amp", mark: false, tile: "A", hue: 350, sat: 60, re: /(?:^|[^a-z])amp(?![a-z])|ampcode|sourcegraph/ },
  { id: "zed", title: "Zed", short: "Zed", mark: false, tile: "Z", hue: 212, sat: 60, re: /(?:^|[^a-z])zed(?![a-z])/ },
];

const byId = new Map<string, Brand>(brands.map(b => [b.id, b]));

/** The brand a hint names (a product, a command, an agent's name or address, a brand id): the
 * first hint that names one wins. "" for none. */
export function brandOf(...hints: (string | undefined)[]): BrandId | "" {
  for (const h of hints) {
    const v = (h ?? "").toLowerCase();
    if (!v) continue;
    for (const b of brands) if (b.re.test(v)) return b.id;
  }
  return "";
}

/** A brand by its id. */
export function brand(id: string): Brand | undefined {
  return byId.get(id);
}

/** The agents the node lists, as much as the brand needs of them. */
export type Listed = { name: string; address: string; node?: string; product?: string };

/** The brand of an agent by its address (agent:claude, agent:x@node: a local agent is listed without
 * its node, usage names it with) or of a brand's name (a cloud agent's): what the node says runs
 * behind it first, then the agent's own name — never its node's (agent:helper@claude-mac is no
 * Claude). */
export function agentBrand(of: string | undefined, agents: Listed[]): BrandId | "" {
  const s = of ?? "";
  const a = s ? agents.find(a => a.address === s || (!!a.node && !a.address.includes("@") && `${a.address}@${a.node}` === s)) : undefined;
  return brandOf(a?.product, nameOf(s));
}

/** The agent's name in an address (agent:name@node → name); a plain name as it is. */
function nameOf(s: string): string {
  const rest = s.includes(":") ? s.slice(s.indexOf(":") + 1) : s;
  return rest.split("@")[0];
}

/** All the brands, for a test or a list. */
export function allBrands(): Brand[] {
  return brands.map(({ re, ...b }) => b);
}
