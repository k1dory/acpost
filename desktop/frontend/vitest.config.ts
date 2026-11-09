import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";

// Unit tests of the app's logic and components, the bindings mocked (src/test/fake.ts), no Wails.
// All of them in happy-dom with Svelte's browser build: the .svelte.ts state is compiled as the
// app's is ($state arrays and objects are proxies). In node it was the server build, plain
// objects, and a test could pass that the app fails.
export default defineConfig({
  plugins: [svelte()],
  resolve: { conditions: ["browser"] },
  test: { include: ["src/**/*.test.ts"], environment: "happy-dom" },
});
