# VX — Velocity Explorations Design System

This is the design system for **Velocity Explorations, Inc. (VX)**, a defense technology company delivering AI, blockchain, software modernization, and emerging tech to U.S. DoD warfighters and commercial clients.

> **Brand archetype:** The Inventor.
> **Mascot:** Peregrine Falcon — speed, precision, focused vision.
> **Tone:** innovative, dependable, open, sharp.

This system is product-agnostic — the same tokens, type, and components carry across the marketing website, **C2 AI** (command-and-control operator platform, dark theme by default), and internal facilitation tools (lighter app surface, light theme default).

## What's in this skill

- [`README.md`](./README.md) — *you are here.* Content fundamentals + visual foundations.
- [`SKILL.md`](./SKILL.md) — entry point. Use this in Claude Code to spin up or update a VX-branded UI.
- [`colors_and_type.css`](./colors_and_type.css) — CSS custom properties for the full token system (colors, type, spacing, radii, shadows, motion) + semantic typography classes.
- [`fonts/`](./fonts) — the four brand webfonts, referenced by `colors_and_type.css`.

The proprietary wordmark face and brand logo PNGs are **not** bundled here — never typeset the wordmark; request official lockups from the VX team.

---

## CONTENT FUNDAMENTALS

VX writes the way an inventor briefs a general: **direct, confident, technical, never theatrical.** Copy is not marketing-y; it's specification-grade.

### Voice
- **Person:** Mostly third-person and imperative. Use *"we"* when speaking for VX as an organization ("We build…"). Use *"you"* sparingly, when addressing the operator or partner directly. Never *"I."*
- **Tense:** Present, active. *"VX delivers."* Not *"VX is delivering"* or *"VX will deliver."*
- **Posture:** State the capability, then the consequence. Avoid hedging ("might," "could help"). Avoid superlatives ("revolutionary," "world-class").

### Tone
- **Innovative** — comfortable with technical specificity.
- **Dependable** — measured, never hyperbolic. The warfighter does not need our enthusiasm.
- **Open** — plain language over jargon when there's a choice; jargon when there isn't.

### Casing
- **Headings:** Sentence case for sentence headings (`Built for the edge of contact.`). **ALL-CAPS** for short labels, eyebrows, callouts (`MISSION-READY`, `C2 AI`).
- **The wordmark** is always all-caps (it's drawn into the typeface).
- **Buttons:** Sentence case (`Request a demo`), or short ALL-CAPS in operator/console contexts (`ENGAGE`, `ABORT`).
- **Product names:** As written. `C2 AI`, not `c2 ai` or `C2-AI`.

### Punctuation
- Oxford comma, always.
- Em-dashes for emphasis — like this — not parenthetical hyphens.
- No exclamation marks. The brand does not shout.

### Emoji & decorative characters
- **No emoji.** Not in product, not in marketing, not in internal tools.
- No decorative unicode (✓, ★, ➜) unless it's a literal symbol with operational meaning.

### Example copy

**On-brand:**
> *Built for the edge of contact. VX C2 AI fuses sensor feeds, doctrine, and operator intent into a single decision surface. Latency under 200ms. Air-gapped or cloud.*

**Off-brand (don't):**
> *🚀 We're revolutionizing the way warfighters experience next-gen AI! Get ready to be amazed!*

### Vocabulary cues
- **Prefer:** *deliver, field, deploy, fuse, harden, sustain, integrate, operator, warfighter, edge, theater, mission, posture, doctrine.*
- **Avoid:** *solutions, leverage, synergy, empower, unlock, journey, magical, delight.*

---

## VISUAL FOUNDATIONS

### Color
Monochromatic by default. Greys do the work; accent colors are interventions, not decoration.

| Token | Hex | Role |
|---|---|---|
| Cod Grey | `#1E1E1E` | Primary dark surface, dark-theme bg. Used in place of pure black. |
| Tundora | `#434343` | Elevated dark surface, dark-theme cards. |
| Boulder | `#757575` | Muted/secondary text on either theme. |
| Silver | `#BEBEBE` | Borders, dividers, disabled states. |
| Alto | `#DADADA` | Light borders, low-emphasis dividers. |
| Wild Sand | `#F5F5F5` | Primary light surface, light-theme bg. Used in place of pure white. |
| **Prussian Blue** | `#00274C` | Accent — depth, authority. **Forbidden on Cod Grey / Tundora / Boulder.** |
| **Philippine Yellow** | `#FFCB03` | Accent — energy, signal. Works on any background. |

**Rules of accent use:**
1. Never pair Prussian Blue with dark greys or near-black. (Use yellow on dark instead.)
2. Use accent for one job per view: a CTA, a status indicator, or a brand mark — not all three.
3. Avoid full `#000` and full `#FFF`. Use Cod Grey and Wild Sand.

### Type
- **Headings + UI + Body** — Inter Tight is the center of the system; use it cohesively everywhere. Semibold/bold with tight tracking (`-0.015em` to `-0.025em`) for headings; regular 400 / medium 500 at `1.5` line-height for body.
- **Space Grotesk** — accent for cyber-security / infosec contexts. Slightly more mechanical. Use sparingly.
- **Chakra Petch** — accent for kinetic / C2 / operator-console contexts and the mono/callsign feel. Sharp diagonals, condensed vertices. Use sparingly.
- **IBM Plex Sans Condensed** — dense data tables, telemetry rows.
- **Wordmark face** — proprietary. Never typeset in markup; always use an official PNG.

The font stacks are wired in `colors_and_type.css` as `--vx-font-display`, `--vx-font-body`, `--vx-font-cyber`, `--vx-font-tactical`, `--vx-font-condensed`, `--vx-font-mono`. Use the semantic classes (`.vx-h1`, `.vx-body`, `.vx-eyebrow`, `.vx-callsign`, …) rather than raw font declarations.

### Spacing
4px base. Scale: `4, 8, 12, 16, 24, 32, 48, 64, 96, 128`. Use `--vx-space-1`…`--vx-space-10`. The scale is rhythmic, not arbitrary — anything off-scale will read wrong.

### Backgrounds
- **No gradient skies.** Surfaces are flat color. Wild Sand light, Cod Grey dark.
- **No repeating patterns / textures.** Visual interest comes from sharp typography, geometry, and the rare yellow signal — not background noise.
- **Full-bleed photography is allowed** in marketing (operators in the field, hardware shots). Imagery should be **cool-toned, slightly desaturated, photojournalistic** — no warm filters, no Instagram grading. A subtle Cod Grey vignette is acceptable when overlaying text.
- **No hand-drawn illustration.** This is not that brand.

### Animation
- **Easing:** `cubic-bezier(0.2, 0.7, 0.2, 1)` for `out`, `cubic-bezier(0.6, 0.05, 0.2, 1)` for `inOut`.
- **Durations:** 120ms (micro: hover), 200ms (base: state changes), 360ms (slow: layout/panel).
- **Style:** crisp fades, short translates (4–8px), no bounce/elastic, no spring overshoot.
- **No looping ambient animations** in product (operator focus matters). Loading uses a thin determinate bar or a clean indeterminate sweep — never a spinner.

### States
- **Hover:** raise contrast — darken light surfaces 4%, lighten dark surfaces 4%. Do not change hue.
- **Press / active:** depress by reducing opacity to `0.92` *and* shifting `translateY(0.5px)`. No color change.
- **Focus:** 2px Philippine Yellow outline with `2px` offset. `--vx-shadow-signal` for inset cases.
- **Disabled:** Silver text on the same background. No graying-out of borders — keep structure visible.

### Borders & rules
- Default border `1px` Alto on light, `1px #2A2A2A` on dark. Strong borders `1px` Silver / Tundora.
- Section rules are full-width hairlines, never dashed or dotted. Sharp corners only.
- **Corner radii are minimal:** `0` (default for cards, dividers, hero blocks), `4px` for buttons & inputs, `6px` for panels. `999px` (pill) reserved for status chips and tags only. **Never rounded-blob aesthetics.**

### Shadows
- Light theme: soft `0 2px 4px rgba(30,30,30,0.08)` for cards, deeper for modals.
- Dark theme: shadows mostly invisible — use border `--vx-border-strong` to separate surfaces instead.
- **No colored shadows.** No glow effects.

### Transparency / blur
- Used only for system chrome (sticky headers when scrolled: `rgba(245,245,245,0.85)` + `backdrop-filter: blur(12px)`).
- Not used as a decorative effect. No frosted cards.

### Layout
- **Container:** max-width 1280px for marketing, 1440px for product. Fluid below.
- **Grid:** 12-column on marketing, 8-column dense for product dashboards.
- **Fixed elements:** top nav can fix; bottom system bar in operator tools fixes. Side rails are usually fixed too. Page content scrolls.
- **Whitespace:** generous on marketing, dense on product. Both use the same spacing scale.

### Cards
- Surface: `--vx-bg-elev`. Border: `1px solid --vx-border`. Often no shadow.
- Radius: `0` (default) or `6px` (interactive cards). Padding: `--vx-space-5` (24px) minimum; `--vx-space-6` (32px) for marketing.
- **Never** a left-only colored accent border. Use a top hairline of `--vx-signal` if the card needs to flag attention.

### Imagery direction
- **B&W photography** preferred for portraits and field shots. **Color** allowed for hardware and screen captures — slightly cool, never warm.
- **Grain:** subtle, optional. No heavy filters.
- **Aspect:** prefer 16:9 hero, 4:5 portrait, 1:1 spot. Avoid 9:16 except for mobile-specific content.

---

## ICONOGRAPHY

### Use Lucide
- **Why:** Open-source, MIT-licensed, sharp 24×24 grid with `2px` strokes — matches the angular precision of the VX mark. CDN-available.
- **Stroke:** `2px` by default. Down to `1.5px` for dense data-grid contexts. Never filled-style. Never duotone.
- **Color:** inherits text color (`currentColor`) — use `--vx-fg`, `--vx-fg-muted`, or `--vx-signal` for emphasis.
- **Size:** 16 (inline-in-text), 20 (default UI), 24 (primary toolbar), 32 (marketing).

```html
<script src="https://unpkg.com/lucide@latest/dist/umd/lucide.min.js"></script>
<i data-lucide="shield-check" style="color: var(--vx-accent); width: 20px; height: 20px"></i>
<script>lucide.createIcons();</script>
```

### No emoji, no decorative unicode
Never — not in copy, product, or slides. No ✓ ✗ ★ ➜ ⚡ in place of real icons. If something needs a check, use the Lucide `check` icon at the right size. This is a brand rule, not a preference.
