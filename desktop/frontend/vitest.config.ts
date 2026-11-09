import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";

// Unit tests of the app's logic and components (happy-dom where a file asks): the bindings are
// mocked (src/test/fake.ts), no Wails. Svelte's browser build, to mount components.
export default defineConfig({
  plugins: [svelte()],
  resolve: { conditions: ["browser"] },
  test: { include: ["src/**/*.test.ts"], environment: "node" },
});
