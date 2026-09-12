# KubeZap brand assets

SVG-first logo package for KubeZap. See `brand-guide.md` in this directory for the color palette, typography, usage rules, and tagline guidance.

| File | Use |
|---|---|
| `kubezap-horizontal.svg` | Primary general-purpose logo (docs, product UI) |
| `kubezap-horizontal-tagline.svg` | Website/marketing contexts |
| `kubezap-vertical.svg` | Presentations, README hero areas, square-ish placements |
| `kubezap-vertical-tagline.svg` | Presentation/marketing contexts |
| `kubezap-icon.svg` | Favicon, app icon, GitHub avatar |
| `kubezap-monochrome.svg` | Printing and constrained-color contexts |
| `kubezap-white.svg` | Dark backgrounds |
| `kubezap-horizontal-auto.svg` | Theme-adaptive horizontal logo for this repo's README — see note below |

The SVGs are real vector files with no embedded raster artwork. The icon (`kubezap-icon.svg`) is the canonical mark; the other layouts are derived from it.

## `kubezap-horizontal-auto.svg`

A derived variant that switches its own fill colors via an embedded `@media (prefers-color-scheme: dark)` rule, so a single relative `<img>` reference renders correctly in both GitHub light and dark themes. This repo is private, so the usual GitHub trick — a `<picture>` element with `<source>` tags pointing at `raw.githubusercontent.com` for the dark variant — doesn't work: that domain 404s without an auth token for private repos. Embedding the color switch in the SVG itself avoids needing any external/absolute URL. Not part of the original brand package; regenerate it from `kubezap-horizontal.svg` + `kubezap-white.svg` if the wordmark design changes.
