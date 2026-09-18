// @ts-check

import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "astro/config"
import react from "@astrojs/react"

import mdx from "@astrojs/mdx";

// https://astro.build/config
export default defineConfig({
  site: "https://jev-pg-dvft.sprites.app",
  vite: {
    plugins: [tailwindcss()],
    server: { allowedHosts: true },
  },
  server: { host: true, port: 8080 },
  integrations: [react(), mdx()],
  markdown: { shikiConfig: { themes: { light: "github-light", dark: "github-dark" } } },
})