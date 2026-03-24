package ticket

import "errors"

// errWrapFmt is the format string for wrapping a sentinel error with a detail value.
const errWrapFmt = "%w: %v"

// Sentinel errors for the ticket store.
var (
	ErrTicketNotFound = errors.New("ticket not found")
	ErrAmbiguousID    = errors.New("ambiguous ticket ID")
	ErrIDCollision    = errors.New("ticket ID collision after retries")
	ErrCorruptYAML    = errors.New("corrupt ticket YAML")
	ErrCycleDetected  = errors.New("dependency cycle detected")
	ErrAlreadyClaimed = errors.New("task already claimed by another owner")
	ErrNotClaimed     = errors.New("task is not claimed")
	ErrNotClaimOwner  = errors.New("caller is not the claim owner")
	ErrNotClaimable   = errors.New("task status is not claimable")
)
