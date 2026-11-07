// The acpost mark: an italic «A» drawn by horizontal stripes, thin and solid at the top, thicker
// and fading towards the bottom. One geometry, written out for every use:
//   ../frontend/src/lib/logo.json      — the app's <Logo> (drawn and animated in the page)
//   extension/media/letterpress-*.svg  — VS Code's empty editor, in the app's look
//   ../build/appicon.svg               — the app's icon
import { writeFileSync, mkdirSync } from "node:fs";

// The letter (its own coordinates, y 300–950): outer outline and its counter, sheared less than
// the drawing it came from.
const outer = [[597, 300], [837, 300], [955, 950], [750, 950], [741, 780], [476, 780], [385, 950], [145, 950]];
const counter = [[717, 440], [736, 655], [566, 655]];
const top = 300, bottom = 950, N = 19;
// Pitch grows from top to bottom; the stripe is ~58 % of it; solid for the top 55 %, then fading.
const p0 = 21, p1 = 2 * (bottom - top) / N - p0;
const stripes = [];
let y = top;
for (let i = 0; i < N; i++) {
  const p = p0 + (p1 - p0) * i / (N - 1);
  const t = (y - top) / (bottom - top);
  const o = t < 0.5 ? 1 : Math.max(0.16, 1 - (t - 0.5) / 0.5 * 0.86);
  stripes.push({ y: +y.toFixed(1), h: +(p * 0.58).toFixed(1), o: +o.toFixed(2) });
  y += p;
}
const pts = a => a.map(([x, y]) => `${x},${y}`).join(" ");
const clip = `M${pts(outer)}Z M${pts(counter)}Z`;
const view = "100 175 900 900";
const x0 = 140, x1 = 960;

mkdirSync("../frontend/src/lib", { recursive: true });
writeFileSync("../frontend/src/lib/logo.json", JSON.stringify({ view, clip, x0, x1, stripes }) + "\n");

const svg = (fill, extra = "", vb = view, wrap = s => s) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${vb}">${extra}
<defs><clipPath id="a"><path clip-rule="evenodd" d="${clip}"/></clipPath></defs>
${wrap(`<g clip-path="url(#a)" fill="${fill}">${stripes.map(s => `<rect x="${x0}" y="${s.y}" width="${x1 - x0}" height="${s.h}" fill-opacity="${s.o}"/>`).join("")}</g>`)}
</svg>
`;
mkdirSync("extension/media", { recursive: true });
// VS Code's letterpress is a faint mark on the empty editor: darker than the page in dark, a
// light grey in light.
writeFileSync("extension/media/letterpress-dark.svg", svg("#000000").replace('<g clip-path', '<g opacity="0.45" clip-path'));
writeFileSync("extension/media/letterpress-light.svg", svg("#000000").replace('<g clip-path', '<g opacity="0.07" clip-path'));
// The icon: the mark in white on the app's black, on macOS's rounded square (824 of 1024, centred).
writeFileSync("../build/appicon.svg", `<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">
<defs><linearGradient id="bg" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#1c1c1a"/><stop offset="1" stop-color="#0b0b0a"/></linearGradient>
<clipPath id="a"><path clip-rule="evenodd" d="${clip}"/></clipPath></defs>
<rect x="100" y="100" width="824" height="824" rx="185" fill="url(#bg)"/>
<rect x="100.5" y="100.5" width="823" height="823" rx="184.5" fill="none" stroke="#ffffff" stroke-opacity="0.08"/>
<g transform="translate(512 512) scale(0.7) translate(-550 -625)"><g clip-path="url(#a)" fill="#f2f1ec">${stripes.map(s => `<rect x="${x0}" y="${s.y}" width="${x1 - x0}" height="${s.h}" fill-opacity="${s.o}"/>`).join("")}</g></g>
</svg>
`);
console.log(N, "stripes; last at", stripes.at(-1));
