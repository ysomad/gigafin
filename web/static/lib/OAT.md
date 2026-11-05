# Oat 1.1.0

Vendored unmodified on November 5, 2026. Both npm's `latest` tag and
[GitHub's latest release](https://github.com/knadh/oat/releases/tag/v1.1.0)
reported version 1.1.0.

- Metadata: https://registry.npmjs.org/@knadh/oat/latest
- Download: https://registry.npmjs.org/@knadh/oat/-/oat-1.1.0.tgz
- `package/oat.min.css` → `oat.min.css`
- `package/oat.min.js` → `oat.min.js`
- `package/LICENSE` → `oat.LICENSE` (MIT)
- Reference: [components](https://oat.ink/components/), [recipes](https://oat.ink/recipes/)

SHA-256:

```text
129d3c21c5af7eb7dcf1c4e6681fab467d5843a31ba602ab3635f0ce5630499b  oat.min.css
63b6924f0bddef4dff107b1326c744368285f1278e784cd2ddbd6d4c98f55bc2  oat.min.js
8e689e72c5de68083003ef22e99ffc875322968251eaf47dd88cbf33969e6326  oat.LICENSE
```

The shared layout loads these local files once, alongside htmx and uPlot.
There is no runtime CDN request or frontend build step. Oat supplies the default
system theme and all application styling; uPlot retains its own vendor stylesheet.
