package ticket

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/runger/attest/internal/state"
)

// Compile-time interface assertion.
var _ state.ClaimableStore = (*Store)(nil)

// DefaultLeaseDuration is the default TTL for new claims.
const DefaultLeaseDuration = 15 * time.Minute

// ClaimInfo holds the claim metadata for a task.
type ClaimInfo struct {
	TaskID      string
	ClaimedBy   string
	Backend     string
	ExpiresAt   time.Time
	HeartbeatAt time.Time
}

// ClaimTask acquires an exclusive claim on a task. The task must be in a
// claimable status (pending or repair_pending) and must not have an active
// (non-expired) claim by another owner.
//
// Same-owner re-claim is idempotent: extends the lease without error.
// Returns ErrAlreadyClaimed if another owner holds a non-expired claim.
func (s *Store) ClaimTask(taskID, ownerID, backend string, lease time.Duration) error {
	resolvedID, err := ResolveID(s.Dir, taskID)
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir, resolvedID+".md")

	return s.withLock(path, func() error {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("%w: %v", ErrTicketNotFound, readErr)
		}
		fm, body, parseErr := splitFrontmatterBody(data)
		if parseErr != nil {
			return parseErr
		}

		// Same-owner re-claim: skip status check, extend lease (pm-001).
		if fm.ClaimedBy == ownerID {
			return s.setClaim(fm, body, path, ownerID, backend, lease)
		}

		// Active claim by other owner: reject unless expired.
		if fm.ClaimedBy != "" {
			expires := parseTime(fm.ClaimExpires)
			if !expires.IsZero() && time.Now().Before(expires) {
				return fmt.Errorf("%w: held by %s until %s", ErrAlreadyClaimed, fm.ClaimedBy, fm.ClaimExpires)
			}
			// Expired — reclaimable, skip status check (task is TaskClaimed from
			// the prior claim, which is not in the claimable set).
			return s.setClaim(fm, body, path, ownerID, backend, lease)
		}

		// Status check: only pending and repair_pending are claimable.
		status := StatusFromTicket(fm.Status, fm.AttestStatus)
		if status != state.TaskPending && status != state.TaskRepairPending {
			return fmt.Errorf("%w: current status is %s", ErrNotClaimable, status)
		}

		return s.setClaim(fm, body, path, ownerID, backend, lease)
	})
}

// setClaim writes claim fields and TaskClaimed status to a ticket file.
func (s *Store) setClaim(fm *Frontmatter, body, path, ownerID, backend string, lease time.Duration) error {
	now := time.Now()
	fm.ClaimedBy = ownerID
	fm.ClaimBackend = backend
	fm.ClaimExpires = formatTime(now.Add(lease))
	fm.ClaimHeartbeat = formatTime(now)
	fm.AttestStatus = string(state.TaskClaimed)
	fm.Status = StatusToTicket(state.TaskClaimed)
	fm.UpdatedAt = formatTime(now)

	out, err := marshalFrontmatterAndBody(fm, body)
	if err != nil {
		return err
	}
	return atomicWrite(path, out)
}

// ReleaseClaim releases the claim on a task. Only the current claim owner
// can release. The task status is set to newStatus.
func (s *Store) ReleaseClaim(taskID, ownerID string, newStatus state.TaskStatus, reason string) error {
	resolvedID, err := ResolveID(s.Dir, taskID)
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir, resolvedID+".md")

	return s.withLock(path, func() error {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("%w: %v", ErrTicketNotFound, readErr)
		}
		fm, body, parseErr := splitFrontmatterBody(data)
		if parseErr != nil {
			return parseErr
		}

		if fm.ClaimedBy == "" {
			return ErrNotClaimed
		}
		if fm.ClaimedBy != ownerID {
			return fmt.Errorf("%w: held by %s", ErrNotClaimOwner, fm.ClaimedBy)
		}

		fm.ClaimedBy = ""
		fm.ClaimBackend = ""
		fm.ClaimExpires = ""
		fm.ClaimHeartbeat = ""
		fm.AttestStatus = string(newStatus)
		fm.Status = StatusToTicket(newStatus)
		if reason != "" {
			fm.StatusReason = reason
		}
		fm.UpdatedAt = formatTime(time.Now())

		out, marshalErr := marshalFrontmatterAndBody(fm, body)
		if marshalErr != nil {
			return marshalErr
		}
		return atomicWrite(path, out)
	})
}

// RenewClaim extends the lease and updates the heartbeat for an active claim.
// Only the claim owner can renew.
func (s *Store) RenewClaim(taskID, ownerID string, lease time.Duration) error {
	resolvedID, err := ResolveID(s.Dir, taskID)
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir, resolvedID+".md")

	return s.withLock(path, func() error {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("%w: %v", ErrTicketNotFound, readErr)
		}
		fm, body, parseErr := splitFrontmatterBody(data)
		if parseErr != nil {
			return parseErr
		}

		if fm.ClaimedBy == "" {
			return ErrNotClaimed
		}
		if fm.ClaimedBy != ownerID {
			return fmt.Errorf("%w: held by %s", ErrNotClaimOwner, fm.ClaimedBy)
		}

		now := time.Now()
		fm.ClaimExpires = formatTime(now.Add(lease))
		fm.ClaimHeartbeat = formatTime(now)
		fm.UpdatedAt = formatTime(now)

		out, marshalErr := marshalFrontmatterAndBody(fm, body)
		if marshalErr != nil {
			return marshalErr
		}
		return atomicWrite(path, out)
	})
}

// ReadClaimsForRun returns claim info for all actively claimed tasks in a run.
func (s *Store) ReadClaimsForRun(runID string) ([]ClaimInfo, error) {
	frontmatters, err := s.readAllFrontmatter()
	if err != nil {
		return nil, err
	}

	var claims []ClaimInfo
	for i := range frontmatters {
		fm := &frontmatters[i]
		if fm.Parent != runID || fm.ClaimedBy == "" {
			continue
		}
		claims = append(claims, ClaimInfo{
			TaskID:      fm.ID,
			ClaimedBy:   fm.ClaimedBy,
			Backend:     fm.ClaimBackend,
			ExpiresAt:   parseTime(fm.ClaimExpires),
			HeartbeatAt: parseTime(fm.ClaimHeartbeat),
		})
	}
	return claims, nil
}

// errNotExpired is a sentinel used internally by ReclaimExpired to signal
// that a claim was renewed between the initial scan and the lock-protected check.
var errNotExpired = errors.New("claim no longer expired")

// ReclaimExpired scans for expired claims in a run and resets those tasks
// to pending status. Returns the list of reclaimed task IDs.
func (s *Store) ReclaimExpired(runID string) ([]string, error) {
	claims, err := s.ReadClaimsForRun(runID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	var reclaimed []string
	for _, claim := range claims {
		if claim.ExpiresAt.IsZero() || !claim.ExpiresAt.Before(now) {
			continue
		}

		// Double-check inside lock to avoid TOCTOU with concurrent RenewClaim.
		path := filepath.Join(s.Dir, claim.TaskID+".md")
		reclaimErr := s.withLock(path, func() error {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr // file gone — skip in outer loop
			}
			fm, body, parseErr := splitFrontmatterBody(data)
			if parseErr != nil {
				return parseErr // corrupt — skip in outer loop
			}

			// Re-check: still expired? (may have been renewed between ReadClaimsForRun and lock)
			expires := parseTime(fm.ClaimExpires)
			if fm.ClaimedBy == "" || expires.IsZero() || !expires.Before(now) {
				return errNotExpired // no longer expired — don't count as reclaimed
			}

			fm.ClaimedBy = ""
			fm.ClaimBackend = ""
			fm.ClaimExpires = ""
			fm.ClaimHeartbeat = ""
			fm.AttestStatus = string(state.TaskPending)
			fm.Status = StatusToTicket(state.TaskPending)
			fm.StatusReason = "claim expired"
			fm.UpdatedAt = formatTime(now)

			out, marshalErr := marshalFrontmatterAndBody(fm, body)
			if marshalErr != nil {
				return marshalErr
			}
			return atomicWrite(path, out)
		})
		if reclaimErr != nil {
			if errors.Is(reclaimErr, errNotExpired) || os.IsNotExist(reclaimErr) {
				continue // expected: claim was renewed or file was deleted
			}
			// Real error (disk full, corrupt YAML, etc.) — collect but continue.
			continue
		}
		reclaimed = append(reclaimed, claim.TaskID)
	}
	return reclaimed, nil
}

// readAllFrontmatter reads all .md files and returns parsed Frontmatters.
// Unlike readAll() which returns []state.Task (no claim fields), this returns
// raw Frontmatter structs that include claim metadata.
func (s *Store) readAllFrontmatter() ([]Frontmatter, error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create tickets dir: %w", err)
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, fmt.Errorf("read tickets dir: %w", err)
	}

	var result []Frontmatter
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(s.Dir, e.Name()))
		if readErr != nil {
			continue
		}
		fm, _, parseErr := splitFrontmatterBody(data)
		if parseErr != nil {
			continue
		}
		result = append(result, *fm)
	}
	return result, nil
}

// marshalFrontmatterAndBody marshals a Frontmatter struct and appends the body.
// Used by claim methods that operate on *Frontmatter directly (not via state.Task).
func marshalFrontmatterAndBody(fm *Frontmatter, body string) ([]byte, error) {
	yamlData, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("marshal frontmatter: %w", err)
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(yamlData)
	buf.WriteString("---\n")
	buf.WriteString(body)
	return buf.Bytes(), nil
}
