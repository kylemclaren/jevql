import { OGImageRoute } from "astro-og-canvas"

// Social card in the hero's look: paper background, ink type, lime accent.
export const { getStaticPaths, GET } = await OGImageRoute({
  param: "route",
  pages: {
    index: {
      title: "Ask your database a real question.",
      description: "WHERE jev(people, 'could work from home')\n\njevql: a psql-shaped CLI for vanilla Postgres.\nNo extension. No proxy. brew install kylemclaren/tap/jevql",
    },
  },
  getImageOptions: (_path, page) => ({
    title: page.title,
    description: page.description,
    bgGradient: [[247, 246, 240]],
    border: { color: [212, 255, 63], width: 36, side: "inline-start" },
    padding: 84,
    font: {
      title: { size: 108, lineHeight: 0.98, weight: "Black", color: [23, 23, 21], families: ["Inter"] },
      description: { size: 34, lineHeight: 1.4, weight: "Medium", color: [102, 100, 94], families: ["JetBrains Mono"] },
    },
    fonts: [
      "https://cdn.jsdelivr.net/fontsource/fonts/inter@latest/latin-900-normal.ttf",
      "https://cdn.jsdelivr.net/fontsource/fonts/jetbrains-mono@latest/latin-500-normal.ttf",
    ],
  }),
})
