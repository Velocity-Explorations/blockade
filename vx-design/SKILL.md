---
name: vx-design
description: Generate well-branded interfaces for Velocity Explorations (VX) — production code or throwaway prototypes/mocks. Contains the brand's design guidelines, color tokens, type system, and webfonts.
user-invocable: true
---

Read `README.md` in this skill for the full brand context, then build.

Files:
- `README.md` — brand voice, content fundamentals, and visual foundations (color, type, spacing, motion, layout, states).
- `colors_and_type.css` — drop-in CSS custom properties for the entire token system plus semantic typography classes. Link or paste it at the top of any HTML/CSS you produce.
- `fonts/` — the four brand webfonts (Inter Tight, Space Grotesk, Chakra Petch, IBM Plex Sans Condensed). `colors_and_type.css` references these with relative `fonts/…` paths — keep the folder next to the CSS, or update the `@font-face` `url()`s to match your asset path.

When updating an existing UI: read `README.md` and `colors_and_type.css`, map the codebase's current colors/type/spacing onto the VX tokens, and apply the brand rules below. Prefer the `--vx-*` custom properties over hard-coded values so the system stays consistent.

If invoked with no other guidance, ask what to build and a few clarifying questions (surface, light or dark theme, marketing vs. operator-console flavor), then act as an expert VX designer.

**Brand rules to never break:**
1. Avoid full black (`#000`) and full white (`#FFF`). Use Cod Grey `#1E1E1E` and Wild Sand `#F5F5F5` instead.
2. Prussian Blue `#00274C` is **forbidden** against any dark grey / near-black. Pair Prussian Blue with light backgrounds only; use Philippine Yellow `#FFCB03` for accent on dark.
3. Never use emoji. Never use decorative unicode (✓ ★ ➜) as icons — use Lucide (see README).
4. Sharp angles, minimal radii. No rounded-blob aesthetics. No gradient skies. No bluish-purple gradients. No frosted/glass cards.
5. Tone is direct, technical, measured. No hype words ("revolutionary," "world-class," "unlock"). No exclamation marks.

> Note: brand logo/wordmark PNGs are **not** bundled in this skill. The wordmark uses a proprietary face — never typeset it; request the official PNG lockups from the VX team when a layout needs the mark.
