// @ts-check

import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "astro/config"
import react from "@astrojs/react"

import mdx from "@astrojs/mdx";

// https://astro.build/config
export default defineConfig({
  site: "https://jevql.fly.dev",
  vite: {
    plugins: [tailwindcss()],
    server: { allowedHosts: true },
    // Islands import these; pre-bundle them up front so the dev optimizer never
    // discovers a new one mid-session and invalidates the island module URLs.
    optimizeDeps: { include: ["react", "react-dom", "react/jsx-runtime", "react/jsx-dev-runtime", "motion/react", "@base-ui/react/toast", "@base-ui/react/button", "lucide-react", "cn", "class-variance-authority"] },
  },
  server: { host: true, port: 8080 },
  integrations: [react(), mdx()],
  markdown: { shikiConfig: { themes: { light: "github-light", dark: "github-dark" } } },
})