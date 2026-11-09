// What an agent wrote, made safe to show: no scripts, no forms, and nothing that loads from the web
// when shown (a picture of http://… in an answer is a tracking pixel). Links stay: a click on one
// is the person's, and the app routes it (App.svelte).
import DOMPurify from "dompurify";

/** An address on the web (loaded from outside the app): http, https, or //host. */
export function remote(url: string): boolean {
  return /^\s*(https?:)?\/\//i.test(url);
}

/** Whether an attribute, kept, would load something from the web as the element is shown. */
export function loadsRemote(tag: string, attr: string, value: string): boolean {
  attr = attr.toLowerCase();
  if (tag.toLowerCase() === "a") return false; // a link loads on a click only
  if (attr === "src" || attr === "poster" || attr === "background" || attr === "href" || attr === "xlink:href" || attr === "data") return remote(value);
  if (attr === "srcset") return value.split(",").some(c => remote(c.trim().split(/\s+/)[0] ?? ""));
  if (attr === "style") return /url\(\s*['"]?\s*(https?:)?\/\//i.test(value);
  return false;
}

let hooked = false;
function hook() {
  if (hooked || !DOMPurify.isSupported) return;
  hooked = true;
  // For every sanitize (Markdown, diagrams): web addresses that load when shown are dropped.
  DOMPurify.addHook("uponSanitizeAttribute", (node, data) => {
    if (loadsRemote(node.nodeName, data.attrName, data.attrValue)) data.keepAttr = false;
  });
}

/** An agent's Markdown, rendered to HTML, made safe. No forms or fields: nothing in a chat may submit. */
export function cleanHtml(html: string): string {
  hook();
  return DOMPurify.sanitize(html, {
    FORBID_TAGS: ["form", "input", "button", "textarea", "select", "iframe", "object", "embed", "base", "meta", "link"],
    FORBID_ATTR: ["formaction", "action", "target"],
  }) as string;
}

/** A drawn diagram's SVG, made safe. */
export function cleanSvg(svg: string): string {
  hook();
  return DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true, svgFilters: true } }) as string;
}

/** The sandbox of an artifact page: nothing runs unless the person allows its scripts. */
export function pageSandbox(scripts: boolean): string {
  return scripts ? "allow-scripts" : "";
}
