import { defineConfig, type Plugin } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import wails from "@wailsio/runtime/plugins/vite";
import { csp } from "./src/lib/csp";

const port = Number(process.env.WAILS_VITE_PORT) || 9245;

// The page's Content-Security-Policy, first in its head (lib/csp.ts says what it allows and why).
function policy(): Plugin {
  let dev = false;
  return {
    name: "acpost-csp",
    configResolved(c) { dev = c.command === "serve"; },
    transformIndexHtml: () => [{ tag: "meta", attrs: { "http-equiv": "Content-Security-Policy", content: csp(dev, port) }, injectTo: "head-prepend" }],
  };
}

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port,
    strictPort: true,
  },
  plugins: [svelte(), wails("./bindings"), policy()],
});
