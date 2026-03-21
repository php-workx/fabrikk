# mill — Branding Plan

**Status:** Draft / Decision pending

## Why "mill"

### The metaphor

A mill is a factory where raw material enters one end and finished product exits the other. That's exactly what this system does: **spec in, verified software out.**

But "mill" carries more nuance than "factory":

- **Precision** — milling is subtractive manufacturing. You remove everything that doesn't match the spec. This maps directly to scope constraints, verification, and the council rejecting work that doesn't trace back to requirements.
- **Mechanical reliability** — a mill runs the same process every time. Deterministic compilation, repeatable verification, structured review. No artisanal hand-waving.
- **Scale-neutral** — a mill can be a small workshop or an industrial operation. The word doesn't over-promise. It works for a single-agent run or a multi-wave orchestration.
- **Heritage** — mills are one of humanity's oldest automation technologies (grain mills, sawmills, textile mills). There's a lineage of "humans designing systems that do reliable work autonomously."

### Why not the alternatives

| Name | Issue |
|------|-------|
| `attest` | Only captures verification — one phase of many |
| `forge` | Implies heat/force/transformation — slightly romantic for what is a controlled process |
| `kiln` | Beautiful but suggests ceramics/art — mill is more engineering |
| `factory` | Too generic, too corporate, too many syllables |
| `foundry` | GitHub Codespaces already uses "codespace" / foundry vibes are taken in Web3 |
| `cast` | Nice but passive — casting waits for the mold to cool. Mill actively processes. |

### CLI ergonomics

```bash
mill run spec.md       # Start a run
mill plan              # View/create execution plan
mill verify            # Run verification pipeline
mill review            # Council review
mill learn             # Agent memory operations
mill status            # Current state
mill claim             # Worker claims a task
mill gc                # Garbage collection
```

Single syllable. Four letters. No conflicts with major CLI tools. `mill` is available on crates.io, npm, PyPI (matters if components get published later).

---

## Logo

### Concept: The Mill Gear

A single, clean gear — but with a subtle twist: the teeth of the gear are shaped like document/page icons (representing specs flowing through the system). Alternatively, the negative space inside the gear forms a checkmark.

### Directions to explore

1. **Geometric gear** — Minimal, flat, monochrome. A gear with 6-8 teeth, one tooth subtly different (highlighted, a checkmark, or a page curl). Works at favicon size. Think: the precision of the process.

2. **Mill wheel** — A water mill wheel seen from the side. More organic, more heritage. Suggests continuous motion — specs flow in like water, turn the wheel, work comes out. Could be stylized into a very clean mark.

3. **Lettermark "m"** — A lowercase "m" where the two humps are stylized as interlocking gears, or where the downstrokes look like factory smokestacks / pillars. Minimal, works as monogram.

4. **Abstract flow** — An arrow or stream entering a geometric shape (the mill) and exiting transformed. Input → process → output in one mark.

### Recommended direction

**Option 1 (Geometric gear)** for the icon/favicon. **Option 3 (Lettermark)** for the wordmark. They complement each other — gear for recognition at small sizes, lettermark for brand presence.

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

3. **"Run it like a mill."**
   - Idiomatic feel. Implies reliability, repeatability, no drama.

4. **"From spec to ship."**
   - Full lifecycle in four words. Action-oriented.

5. **"Autonomous runs. Verified output."**
   - More technical. Says what it does for the agent-tooling audience.

### Recommended

**Primary:** "Spec in. Software out." — for landing pages, README hero, conference slides.
**Secondary:** "Software, to spec." — for tagline under logo, GitHub description, social bios.

---

## Icon (app icon / favicon / CLI)

### CLI output identity

```text
⚙ mill v0.1.0
```

Use the gear unicode character (⚙ U+2699) as the CLI prefix. Simple, renders everywhere, reinforces the brand without custom font requirements.

### Favicon

The geometric gear mark at 32x32 and 16x16. Monochrome (charcoal on transparent) for light backgrounds, inverted for dark. Must be recognizable at pixel scale — avoid detail, maximize silhouette.

### Social / GitHub avatar

Gear mark on the steel-blue background, with the amber accent on one tooth (the "verified" tooth). Square crop, no text.

---

## Positioning & marketing

### The pitch (30 seconds)

> Coding agents are powerful but unconstrained. They drift from requirements, skip verification, and produce work that doesn't match what was asked for.
>
> **mill** is the factory floor for autonomous coding. It takes a spec, compiles it into a task graph, orchestrates agents to execute it, and independently verifies that every output traces back to an approved requirement. Nothing ships unless it passes the quality gate.
>
> Spec in. Software out.

### Target audiences

| Audience | Message | Channel |
|----------|---------|---------|
| **Agent-tool builders** | "The missing orchestration layer for your agents" | GitHub, HN, developer Discord servers |
| **Engineering leads** | "Autonomous coding with audit trails" | Blog posts, LinkedIn, conference talks |
| **Solo developers** | "Ship faster without losing control" | Twitter/X, Reddit, indie hacker communities |
| **AI-curious orgs** | "Production-grade agent orchestration" | Case studies, enterprise landing page |

### Differentiation — what makes mill fresh

1. **Spec-driven, not prompt-driven.** Most agent tools start from a chat prompt. mill starts from a structured spec with requirements, acceptance criteria, and risk profiles. This is engineering, not vibes.

2. **Verification is built in, not bolted on.** The council review (multi-model, multi-persona) and evidence-based verification aren't optional plugins — they're the core loop. Every output has provenance.

3. **Deterministic compilation.** The spec-to-task-graph step is rule-based, not LLM-generated. You can diff plans, reproduce them, and reason about them. This is rare in the agent space.

4. **Single binary, minimal deps.** In a world of agent frameworks that need Docker, Redis, vector databases, and three API keys — mill is one Go binary with 4 dependencies. Refreshingly simple.

5. **Industrial metaphor in an artisanal market.** Everyone else is selling "AI pair programmers" and "copilots." mill says: this is a factory. Specs go in, verified software comes out. No magic, no personality, just process. That honesty is the brand.

### Launch channels

| Phase | Action | Timeline |
|-------|--------|----------|
| **Pre-launch** | Rename repo, update README with new branding, "Coming soon" landing page | Before v1 |
| **Soft launch** | GitHub release, HN "Show HN" post, Twitter thread explaining the philosophy | v1.0 |
| **Community** | Discord or GitHub Discussions, example specs + runs, "mill gallery" of verified outputs | v1.0 + 2 weeks |
| **Content** | Blog series: "Why spec-driven beats prompt-driven", "What coding agents get wrong", "Verification as a first-class citizen" | Ongoing |
| **Conference** | Lightning talk: live demo of spec → verified code in 5 minutes | When ready |

### The "Show HN" angle

> **Show HN: Mill — A spec-driven factory for autonomous coding agents**
>
> Most agent orchestration tools optimize for "give the AI a prompt and let it go." Mill goes the other direction: you write a structured spec, mill compiles it into a deterministic task graph, agents execute in constrained lanes, and every output is independently verified against requirements before it ships.
>
> Single Go binary, 4 dependencies, no LLM in the planning step. The council review uses multiple models with different personas to catch what automated checks miss.
>
> [link] | [example spec → output walkthrough]

This positions mill as the contrarian take in the agent space — rigor over vibes — which is exactly the kind of thing HN rewards.

---

## Open questions

- [ ] Domain availability: `mill.dev`? `usemill.com`? `millrun.dev`?
- [ ] Package name conflicts: check Go module path, npm, Homebrew formula
- [ ] Existing tools named "mill": there's a Java/Scala build tool called Mill — assess confusion risk
- [ ] When to rename: before or after v1? (Recommendation: before — avoid redirects and stale references)
