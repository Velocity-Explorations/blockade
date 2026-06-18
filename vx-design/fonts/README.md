# Fonts

All four families are bundled and wired into `../colors_and_type.css` via `@font-face` with relative `fonts/…` paths. Keep this folder next to the CSS.

| Family | Role | Files |
|---|---|---|
| **Inter Tight** | Headings, UI, body — the center of the system | `InterTight-VariableFont_wght.ttf` (100–900), `InterTight-Italic-VariableFont_wght.ttf` |
| **Space Grotesk** | Cyber / infosec accent | `SpaceGrotesk-VariableFont_wght.ttf` (300–700) |
| **Chakra Petch** | Kinetic / C2 / operator-console accent + mono/callsign | `ChakraPetch-*` (Light → Bold, roman + italic) |
| **IBM Plex Sans Condensed** | Dense data tables, telemetry rows | `IBMPlexSansCondensed-*` (Thin → Bold) |

Only the weights `colors_and_type.css` actually loads are included. If you add new weights/styles to the CSS, drop the matching `.ttf` here.

**The proprietary VX wordmark face is not included** — never typeset the wordmark; use an official PNG lockup from the VX team.

All bundled families ship under the SIL Open Font License.
