// Copies the app's fonts (Onest for the interface, JetBrains Mono for code) into the IDE
// extension: the page loads them from the extension (its CSP takes fonts only from its own
// server), the same files the app uses.
import { copyFileSync, mkdirSync, writeFileSync } from "node:fs";

const from = "../frontend/node_modules/@fontsource";
mkdirSync("extension/fonts", { recursive: true });
// The list of them (fonts.json) is the one the app reads to make the page's @font-face rules.
const faces = [];
const copy = (src, file, family, weight) => {
  copyFileSync(src, `extension/fonts/${file}`);
  faces.push({ family, file, weight });
};
for (const s of ["latin", "cyrillic"]) {
  copy(`${from}-variable/onest/files/onest-${s}-wght-normal.woff2`, `onest-${s}.woff2`, "Onest", "100 900");
  for (const w of [400, 500]) copy(`${from}/jetbrains-mono/files/jetbrains-mono-${s}-${w}-normal.woff2`, `jetbrains-mono-${s}-${w}.woff2`, "JetBrains Mono", String(w));
}
writeFileSync("extension/fonts/fonts.json", JSON.stringify(faces, null, 2) + "\n");
console.log("fonts copied");
