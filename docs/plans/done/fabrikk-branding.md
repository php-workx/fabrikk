# fabrikk — Branding Plan

**Status:** Decided — `fabrikk` (project + binary), `fab` (alias)
**Supersedes:** mill branding proposal (rejected — Java/Scala build tool conflict, metaphor too narrow)

---

## Why "fabrikk"

### The metaphor

A Fabrikk is a factory — a coordinated production facility where raw material enters, passes through multiple specialized stations with quality control at each stage, and finished product exits. That's exactly what this system does:

- **Spec review** — incoming inspection
- **Execution planning** — production scheduling
- **Task compilation** — work order generation
- **Agent implementation** — assembly line workers
- **Verification** — quality control
- **Council review** — independent audit
- **Approval** — release gate

A mill grinds. A forge strikes. A factory *orchestrates*. The system isn't a single transformation — it's a multi-stage production pipeline with feedback loops, and "fabrikk" captures that.

### Why the double-k

"Fabrikk" is the Norwegian/Danish spelling of "factory." The double-k:

- **Distinguishes from "Fabrik"** — avoids confusion with fabrik.io (portfolio builder), fabrik.com (adtech), fabrik.space (3D platform), and the historical Xerox/Apple Fabrik visual programming language.
- **Creates a unique namespace** — all package registries (npm, PyPI, crates.io, Go module), Homebrew, and desirable domains (fabrikk.dev, fabrikk.sh, fabrikk.io) are available.
- **Catches the eye** — the unusual spelling makes people ask about it. "It's Norwegian for factory" is a one-sentence brand story.

### Why not the other candidates

| Name | Issue |
|------|-------|
| `attest` | Only captures verification — one phase of many |
| `mill` | Java/Scala build tool (Mill) with same name. Metaphor too narrow — mills grind, they don't orchestrate multi-stage production |
| `werk` | Rust build tool (simonask/werk, 316 stars, HN featured). Go AI dev tool (zackbart/werk) in identical niche |
| `forge` | Foundry Forge (Ethereum, 10.2k stars), Electron Forge, Laravel Forge, Minecraft Forge — fatally polluted |
| `lathe` | Clean namespace but metaphor is single-stage precision machining, not multi-station production |
| `plant` | PlantUML (12.8k stars) dominates all "plant + developer tool" searches permanently |
| `fabrik` | Single-k: fabrik.io, fabrik.com, fabrik.space, fabrik.software all taken. npm/PyPI squatted |
| `gauge` | ThoughtWorks Gauge (3.2k stars, Go CLI test tool, gauge.org domain) — fatal overlap |
| `rivet` | Two AI agent platforms (combined ~10k stars) own the name in the exact same space |

### Namespace audit (2026-03-24)

| Registry | Status |
|----------|--------|
| npm | Available |
| PyPI | Available |
| crates.io | Available |
| Go module | Available |
| Homebrew | Available |
| fabrikk.dev | Likely available |
| fabrikk.sh | Likely available |
| fabrikk.io | Likely available |
| GitHub "fabrikk" | 38 results, all Norwegian-language repos or dormant. No CLI/dev tools |

### CLI ergonomics

```bash
# Full name (project binary)
fabrikk run spec.md       # Start a run
fabrikk plan              # View/create execution plan
fabrikk verify            # Run verification pipeline
fabrikk review            # Council review
fabrikk learn             # Agent memory operations
fabrikk status            # Current state
fabrikk claim             # Worker claims a task

# Short alias
fab run spec.md
fab plan
fab review
fab status
```

**Binary:** `fabrikk` (installed via `brew install fabrikk` or `go install`)
**Alias:** `fab` (symlink installed alongside the binary)

The alias follows the `ripgrep`/`rg` pattern — project named `fabrikk`, daily usage via `fab`. Both always work.

**Note on `fab` conflict:** Python's Fabric deployment tool uses `fab` as its CLI command. Fabric is Python 2 era; Fabric v3 still uses `fab` but its mindshare has declined since Ansible/Docker took over. Most Go/agent-tooling users won't have it installed. If both are present, the user's PATH order resolves it — or they use `fabrikk` explicitly.

---

## Logo

### Concept: The Factory Mark

A stylized factory building in profile — clean geometric lines, a single smokestack, with a subtle checkmark or verification mark integrated into the roofline. Suggests production, process, and quality in one mark.

### Directions to explore

1. **Geometric factory** — Minimal, flat, monochrome. A rectangular building with a triangular roof and one smokestack. The smokestack emits a checkmark instead of smoke. Works at favicon size.

2. **Assembly line** — A horizontal flow: document icon → three connected stations (small squares) → verified output (checkmark badge). Input → process → output in one mark. More literal, more explanatory.

3. **Lettermark "f"** — A lowercase "f" where the crossbar extends into a production line. Or the top curve of the "f" is shaped like a factory roofline. Minimal, works as monogram.

4. **Double-k mark** — The two k's from "fabrikk" stylized as interlocking components or assembly stations. Unique to the brand, references the distinctive spelling.

### Recommended direction

**Option 1 (Geometric factory)** for the icon/favicon. **Option 3 (Lettermark)** for the wordmark. The factory silhouette is instantly recognizable at small sizes; the lettermark carries the brand in text contexts.

### Color palette

| Role | Color | Hex | Rationale |
|------|-------|-----|-----------|
| Primary | Steel blue | `#4A7C9B` | Engineering, precision, trust |
| Accent | Amber/gold | `#D4943A` | The spark — verified, approved, quality |
| Dark | Charcoal | `#1E1E2E` | Terminal-native, developer-first |
| Light | Off-white | `#F5F3EF` | Paper/spec — warm, not clinical |

Steel blue + amber = industrial but not cold. Feels like a workshop with good lighting.

---

## Slogan

### Primary candidates

1. **"Spec in. Software out."**
   - Direct. Explains the product in four words. Factory metaphor without saying factory.

2. **"Software, to spec."**
   - Double meaning: built according to spec / delivered to specification. Confident and brief.

3. **"From spec to ship."**
   - Full lifecycle in four words. Action-oriented.

4. **"The factory floor for autonomous coding."**
   - Explicit. Says what it is. Works as a subtitle.

5. **"Autonomous runs. Verified output."**
   - More technical. Says what it does for the agent-tooling audience.

### Recommended

**Primary:** "Spec in. Software out." — for landing pages, README hero, conference slides.
**Secondary:** "Software, to spec." — for tagline under logo, GitHub description, social bios.

---

## Icon (app icon / favicon / CLI)

### CLI output identity

```text
🏭 fabrikk v0.1.0
```

Use the factory unicode character (🏭 U+1F3ED) as the CLI prefix. Alternatively, the gear (⚙ U+2699) if the factory emoji renders poorly in some terminals.

### Favicon

The geometric factory mark at 32x32 and 16x16. Monochrome (charcoal on transparent) for light backgrounds, inverted for dark. Must be recognizable at pixel scale — avoid detail, maximize silhouette.

### Social / GitHub avatar

Factory mark on the steel-blue background, with the amber accent on the checkmark/smokestack. Square crop, no text.

---

## Positioning & marketing

### The pitch (30 seconds)

> Coding agents are powerful but unconstrained. They drift from requirements, skip verification, and produce work that doesn't match what was asked for.
>
> **fabrikk** is the factory floor for autonomous coding. It takes a spec, compiles it into a task graph, orchestrates agents to execute it, and independently verifies that every output traces back to an approved requirement. Nothing ships unless it passes the quality gate.
>
> Spec in. Software out.

### Target audiences

| Audience | Message | Channel |
|----------|---------|---------|
| **Agent-tool builders** | "The missing orchestration layer for your agents" | GitHub, HN, developer Discord servers |
| **Engineering leads** | "Autonomous coding with audit trails" | Blog posts, LinkedIn, conference talks |
| **Solo developers** | "Ship faster without losing control" | Twitter/X, Reddit, indie hacker communities |
| **AI-curious orgs** | "Production-grade agent orchestration" | Case studies, enterprise landing page |

### Differentiation — what makes fabrikk fresh

1. **Spec-driven, not prompt-driven.** Most agent tools start from a chat prompt. fabrikk starts from a structured spec with requirements, acceptance criteria, and risk profiles. This is engineering, not vibes.

2. **Verification is built in, not bolted on.** The council review (multi-model, multi-persona) and evidence-based verification aren't optional plugins — they're the core loop. Every output has provenance.

3. **Deterministic compilation.** The spec-to-task-graph step is rule-based, not LLM-generated. You can diff plans, reproduce them, and reason about them. This is rare in the agent space.

4. **Single binary, minimal deps.** In a world of agent frameworks that need Docker, Redis, vector databases, and three API keys — fabrikk is one Go binary with minimal dependencies. Refreshingly simple.

5. **Factory metaphor in an artisanal market.** Everyone else is selling "AI pair programmers" and "copilots." fabrikk says: this is a factory. Specs go in, verified software comes out. No magic, no personality, just process. That honesty is the brand.

### Launch channels

| Phase | Action | Timeline |
|-------|--------|----------|
| **Pre-launch** | Rename repo, update README with new branding, secure domains | Before v1 |
| **Soft launch** | GitHub release, HN "Show HN" post, Twitter thread explaining the philosophy | v1.0 |
| **Community** | Discord or GitHub Discussions, example specs + runs, "fabrikk gallery" of verified outputs | v1.0 + 2 weeks |
| **Content** | Blog series: "Why spec-driven beats prompt-driven", "What coding agents get wrong", "Verification as a first-class citizen" | Ongoing |
| **Conference** | Lightning talk: live demo of spec → verified code in 5 minutes | When ready |

### The "Show HN" angle

> **Show HN: Fabrikk — A spec-driven factory for autonomous coding agents**
>
> Most agent orchestration tools optimize for "give the AI a prompt and let it go." Fabrikk goes the other direction: you write a structured spec, fabrikk compiles it into a deterministic task graph, agents execute in constrained lanes, and every output is independently verified against requirements before it ships.
>
> Single Go binary, minimal dependencies, no LLM in the planning step. The council review uses multiple models with different personas to catch what automated checks miss.
>
> [link] | [example spec → output walkthrough]

This positions fabrikk as the contrarian take in the agent space — rigor over vibes — which is exactly the kind of thing HN rewards.

---

## Rename checklist

- [x] Rename repo: `php-workx/attest` → `php-workx/fabrikk`
- [x] Update Go module path: `github.com/php-workx/fabrikk`
- [x] Rename binary: `cmd/attest/` → `cmd/fabrikk/`
- [x] Add `fab` symlink in build scripts (justfile)
- [x] Update all internal references: `.attest/` → `.fabrikk/`, CLAUDE.md, README, help text
- [x] Set up goreleaser with Homebrew tap (`php-workx/homebrew-tap`)
- [x] Update CI/CD pipeline references
- [x] Add `--version` flag with ldflags
- [x] Rename spec files: `attest-*.md` → `fabrikk-*.md`
- [x] Rename `attest_status` YAML field → `extended_status`
- [x] Fix GenerateID prefix derivation (project dir, not .tickets/)
- [ ] Secure domains: fabrikk.dev, fabrikk.sh (deferred — not blocking v1)
- [ ] Register on npm/PyPI/crates.io (deferred — placeholder protection)
