// @ts-check

import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "astro/config"
import react from "@astrojs/react"

// https://astro.build/config
export default defineConfig({
  vite: {
    plugins: [tailwindcss()],
    server: { allowedHosts: true },
  },
  server: { host: true, port: 8080 },
  integrations: [react()],
})
