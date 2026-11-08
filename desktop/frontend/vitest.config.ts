import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";

// Unit tests of the app's logic: the bindings are mocked (src/test/fake.ts), no Wails, no window.
export default defineConfig({
  plugins: [svelte()],
  resolve: { conditions: ["browser"] },
  test: { include: ["src/**/*.test.ts"], environment: "node" },
});
