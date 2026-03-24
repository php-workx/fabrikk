---
id: att-j63b
status: closed
deps: []
links: []
created: 2026-03-24T05:17:32Z
type: task
priority: 2
assignee: Ronny Unger
tags: [testing, normalization, P2]
---
# Add missing normalization test coverage

## What's wrong

Three tests required by the spec are missing:

1. **Canonical pass-through test** (engine_test.go): Spec requires a test that
   a document with all 8 canonical headings passes through byte-for-byte when
   noNormalize=false. This verifies the fast path that skips agent invocation.
   Current TestDraftTechnicalSpec_NoNormalizePassThrough tests noNormalize=true,
   which is a different code path.

2. **CLI --no-normalize test** (commands_test.go): Spec line 183 requires
   TestCmdTechSpecDraftNoNormalize in cmd/fabrikk/commands_test.go. Not implemented.

3. **stdin fallback threshold test** (agentcli_test.go): Spec line 75 mentions
   testing the 32000-byte stdin threshold. Not implemented.

## Risk

Untested fast path: if hasCanonicalTechnicalSpecHeadings is accidentally broken,
canonical docs would be sent to the agent unnecessarily (wasted time + money).
The CLI flag test gap means the --no-normalize flag integration is only tested
at the engine level, not end-to-end.

## Acceptance criteria

- TestDraftTechnicalSpec_CanonicalPassThrough exists: writes doc with all 8 headings,
  calls DraftTechnicalSpec(ctx, path, false), verifies output == input byte-for-byte
  and InvokeFunc was NOT called
- TestCmdTechSpecDraftNoNormalize exists in commands_test.go: exercises the CLI path
- TestInvoke_StdinFallback exists in agentcli_test.go: verifies prompts >32000 bytes
  use stdin (mock exec to capture args, verify prompt is NOT in args)

## Test cases

1. Canonical pass-through: write spec with "## 1. Technical context" through
   "## 8. Approval", call DraftTechnicalSpec(ctx, path, false), assert output
   bytes == input bytes, assert mock InvokeFunc call count == 0
2. CLI no-normalize: call cmdTechSpec(ctx, ["draft", runID, "--from", path,
   "--no-normalize"]), verify spec written unchanged
3. Stdin threshold: mock exec.Command, invoke with 40000-byte prompt, verify
   prompt not in command args (piped via stdin instead)

