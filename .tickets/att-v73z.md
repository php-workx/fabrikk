---
id: att-v73z
status: closed
deps: []
links: []
created: 2026-03-24T09:58:09Z
type: task
priority: 2
assignee: Ronny Unger
tags: [rebrand, P2]
---
# Rename attest_status YAML field to extended_status

## What's wrong

The YAML field attest_status in ticket frontmatter is a product-branded name for
what is really a generic concept: the fine-grained 9-value status that extends
the tk-native 3-value status field.

## What to rename

- YAML tag: attest_status → extended_status
- Go field: AttestStatus → ExtendedStatus
- Map variable: attestToTicketStatus → extendedToTicketStatus
- Map variable: validAttestStatuses → validExtendedStatuses
- Function: StatusFromTicket(tkStatus, attestStatus) → StatusFromTicket(tkStatus, extendedStatus)
- All comments referencing "attest-specific" or "attest status"
- Existing .tickets/*.md files: sed -i 's/extended_status:/extended_status:/g'

## Files affected

- internal/ticket/format.go (struct, maps, functions, comments)
- internal/ticket/format_test.go (test data, assertions)
- internal/ticket/store.go (comment)
- internal/ticket/claim.go (field references)
- internal/ticket/claim_test.go (assertions checking field in YAML output)
- .tickets/*.md (existing ticket files)

## No backward compat needed

This is a pre-v1 project. Existing ticket files can be updated via sed.

## Migration of existing ticket files (pm-002)

The sed migration MUST be in the same commit as the Go code changes.
If forgotten, ReadTask silently falls back to tk-native status mapping —
claimed tasks appear as pending, implementing tasks appear as claimed, etc.

Run in the commit:
```bash
sed -i '' 's/extended_status:/extended_status:/g' .tickets/*.md
```

Verify after:
```bash
grep -l 'attest_status' .tickets/*.md  # must return empty
grep -l 'extended_status' .tickets/*.md  # must return all ticket files with status
```

## Acceptance criteria

- grep 'attest_status' internal/ticket/ returns empty
- grep 'AttestStatus' internal/ticket/ returns empty
- grep 'attestToTicketStatus\|validAttestStatuses' returns empty
- grep 'attest_status' .tickets/*.md returns empty (migration applied)
- All tests pass
- Existing .tickets/*.md files use extended_status

