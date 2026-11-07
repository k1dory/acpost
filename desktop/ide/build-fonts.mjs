// Copies the app's fonts (Onest for the interface, JetBrains Mono for code) into the IDE
// extension: the page loads them from the extension (its CSP takes fonts only from its own
// server), the same files the app uses.
import { copyFileSync, mkdirSync } from "node:fs";

const from = "../frontend/node_modules/@fontsource";
mkdirSync("extension/fonts", { recursive: true });
for (const s of ["latin", "cyrillic"]) {
  copyFileSync(`${from}-variable/onest/files/onest-${s}-wght-normal.woff2`, `extension/fonts/onest-${s}.woff2`);
  for (const w of [400, 500]) copyFileSync(`${from}/jetbrains-mono/files/jetbrains-mono-${s}-${w}-normal.woff2`, `extension/fonts/jetbrains-mono-${s}-${w}.woff2`);
}
console.log("fonts copied");
