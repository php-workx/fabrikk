# Go Agent Framework Fit Analysis for `attest`

Date: 2026-03-12
Status: Research note
Scope: Evaluate Eino, tRPC-Agent-Go, and Beluga AI for possible reuse inside `attest`.

## Summary

All three projects have useful ideas. None of them are a clean drop-in foundation for `attest`.

Best immediate takeaway:

- **Eino** is the strongest source of ideas for typed workflow composition, callback aspects, and interrupt/resume.
- **tRPC-Agent-Go** is the strongest source of ideas for runner/event models, evaluation, artifacts, and telemetry.
- **Beluga AI** is the strongest source of ideas for durable workflow separation and a layered architecture, but it is much less mature than the marketing suggests.

Best adoption recommendation:

- do **not** replace `attest` with any of these frameworks
- do **not** adopt one wholesale in v1
- selectively borrow concepts into `attest`, especially for:
  - typed councilflow orchestration
  - event streams and artifacts
  - later evaluation and telemetry

Most important mismatch across all three:

- they are provider/API oriented
- `attest` is intentionally local CLI and subscription oriented

That means none of them directly solve our main adoption requirement.

## 1. Eino

Primary sources:

- GitHub repo: [cloudwego/eino](https://github.com/cloudwego/eino)
- Docs overview: [Eino Overview](https://www.cloudwego.io/docs/eino/overview/)

Current public repo signal observed:

- about **10k stars**
- **316 commits**
- major top-level packages include `adk`, `components`, `compose`, `flow`, and `callbacks`

Relevant capabilities:

- ADK for agents with multi-agent coordination, context management, and interrupt/resume
- composition primitives for chain, graph, and workflow
- callback aspects for logging, tracing, and metrics
- stream processing built into orchestration

Evidence:

- GitHub repo shows `adk`, `components`, `compose`, and `flow` packages plus ~10k stars and 316 commits: [cloudwego/eino](https://github.com/cloudwego/eino)
- README states ADK includes tool use, multi-agent coordination, context management, and interrupt/resume: [cloudwego/eino README lines 310-323](https://github.com/cloudwego/eino)
- README states composition includes graphs and workflows and can expose compositions as tools: [cloudwego/eino README lines 371-399](https://github.com/cloudwego/eino)
- README highlights callback aspects and interrupt/resume: [cloudwego/eino README lines 414-423](https://github.com/cloudwego/eino)

What looks useful for `attest`:

- typed workflow composition ideas
- explicit callback/aspect hooks for observability
- interrupt/resume semantics for HITL checkpoints
- clean split between deterministic composition and agent behavior

What does **not** fit well:

- Eino is a broad LLM application framework, not a narrow council/review layer
- examples are provider/API based, for example OpenAI API key usage in quick start
- adopting Eino would push `attest` toward framework ownership instead of repo-owned orchestration

Evidence of API/provider orientation:

- quick start uses `APIKey: os.Getenv("OPENAI_API_KEY")`: [cloudwego/eino README lines 321-324](https://github.com/cloudwego/eino)

Verdict:

- **Useful as an idea source**
- **Not a good core dependency for v1**
- if we borrow from one framework conceptually, Eino is probably the best source for `councilflow` and future workflow abstractions

## 2. tRPC-Agent-Go

Primary sources:

- GitHub repo: [trpc-group/trpc-agent-go](https://github.com/trpc-group/trpc-agent-go)
- Docs site: [tRPC-Agent-Go docs](https://trpc-group.github.io/trpc-agent-go/)

Current public repo signal observed:

- about **1k stars**
- **1,209 commits**

Relevant capabilities:

- multi-agent orchestration with chain, parallel, cycle, and graph patterns
- runner abstraction
- persistent memory
- evaluation and benchmarks
- artifacts
- AG-UI and A2A integration
- telemetry and tracing

Evidence:

- GitHub repo shows ~1k stars and 1,209 commits: [repo lines with stars and commits](https://github.com/trpc-group/trpc-agent-go)
- repo structure includes `artifact`, `evaluation`, `event`, `graph`, `memory`: [repo tree lines 196-260](https://github.com/trpc-group/trpc-agent-go)
- docs show memory service at runner level: [memory example](https://github.com/trpc-group/trpc-agent-go)
- docs show evaluation/benchmarks: [evaluation section](https://github.com/trpc-group/trpc-agent-go)
- docs show AG-UI, artifacts, A2A, and telemetry: [AG-UI and telemetry lines](https://github.com/trpc-group/trpc-agent-go), [repo README sections](https://github.com/trpc-group/trpc-agent-go)

What looks useful for `attest`:

- runner + event stream model
- artifacts as first-class outputs
- evaluation harness ideas for future spec-conformance scoring
- telemetry integration and OpenTelemetry hooks
- graph/parallel agent patterns as implementation inspiration

What does **not** fit well:

- it is broader than the part of `attest` we need
- it is oriented toward agent runtime construction, not repo-owned spec-attestation workflows
- docs and examples are again provider/API oriented

Evidence of API/provider orientation:

- docs explicitly tell users to export `OPENAI_API_KEY`: [Agent docs](https://trpc-group.github.io/trpc-agent-go/agent/)

Verdict:

- **Strong idea source**
- especially good for future `events.jsonl`, evaluation, and artifact handling
- **Still not a clean v1 dependency**

## 3. Beluga AI

Primary sources:

- Website: [beluga-ai.org](https://beluga-ai.org/)
- GitHub repo: [lookatitude/beluga-ai](https://github.com/lookatitude/beluga-ai)

Current public repo signal observed:

- website markets a broad production-ready platform
- GitHub repo currently shows only **9 stars**
- GitHub repo shows **167 commits**

That mismatch matters.

Relevant capabilities claimed:

- orchestration with chains, graphs, routers, and workflow
- durable workflows
- guardrails
- MCP and A2A
- observability
- memory
- layered architecture

Evidence:

- website positions it as “production AI agents” with streaming-first design and 22+ providers: [beluga-ai.org](https://beluga-ai.org/)
- website lists MCP, A2A, guardrails, durable workflows, and observability: [feature sections](https://beluga-ai.org/)
- GitHub repo currently shows 9 stars and 167 commits: [lookatitude/beluga-ai](https://github.com/lookatitude/beluga-ai)
- repo README says durable execution engine separates deterministic orchestration from non-deterministic activities: [beluga-ai README lines 431-440](https://github.com/lookatitude/beluga-ai)

What looks useful for `attest`:

- the architecture idea of separating deterministic orchestration from non-deterministic activities is very relevant
- layered package layout is thoughtfully aligned with infrastructure concerns
- durable workflow language maps well to our detached-run semantics

What does **not** fit well:

- the project is far less battle-tested in public than the marketing implies
- the scope is very broad, much broader than what `attest` needs
- it is still provider/API oriented in examples

Evidence of API/provider orientation:

- README quick start uses `APIKey: os.Getenv("OPENAI_API_KEY")`: [beluga-ai README lines 470-474](https://github.com/lookatitude/beluga-ai)

Verdict:

- **Interesting architecture source**
- **Too immature to use as a foundation**
- borrow ideas only

## 4. What is most useful to borrow

### From Eino

- typed composition patterns
- callback/aspect hooks
- interrupt/resume semantics
- graph/workflow separation

### From tRPC-Agent-Go

- runner and event model
- artifacts as first-class outputs
- evaluation and benchmark structure
- telemetry integration

### From Beluga AI

- “deterministic orchestration vs non-deterministic activities” framing
- layered package decomposition
- durable workflow language

## 5. What we should not do

Do not:

- adopt any of these as the `attest` runtime
- replace `.attest` canonical artifacts with framework-native state
- pull in a broad framework just to avoid writing `internal/councilflow`
- assume any of them solve local CLI subscription use

## 6. Best fit for `attest`

If the question is “which one is most aligned with what we are building?”:

1. **Eino** is the best conceptual fit for typed orchestration and HITL patterns
2. **tRPC-Agent-Go** is the best conceptual fit for operational extras like events, artifacts, and evals
3. **Beluga AI** is the best conceptual fit for durable workflow architecture, but weakest in maturity

If the question is “which one should we actually depend on in v1?”:

- **None**

## 7. Recommendation

Stay with the current `attest` direction:

- Go-native
- repo-owned state
- local CLI/subscription execution
- narrow `internal/councilflow`

Then selectively borrow:

- Eino-style typed composition ideas
- tRPC-Agent-Go-style event/artifact/eval ideas
- Beluga-style deterministic-vs-nondeterministic workflow framing

## Bottom Line

These frameworks are useful research inputs, not good foundations for `attest` v1.

They validate that:

- Go is now a serious language for agent frameworks
- workflow composition, events, memory, eval, and telemetry are all becoming standard

But they do not remove our core requirement:

- `attest` must stay local-CLI and subscription friendly

That is still our differentiator, and it keeps pushing us toward a smaller in-house orchestration layer rather than a broad external framework.
