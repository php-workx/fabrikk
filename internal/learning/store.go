package learning

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"

	"github.com/runger/attest/internal/state"
)

// DefaultLeaseDuration is unused here but exported for consistency.
const (
	decayThreshold = 30 * 24 * time.Hour // 30 days
	decayAmount    = 0.05
)

// ErrCorruptLearningStore indicates the JSONL store has corrupt lines.
var ErrCorruptLearningStore = errors.New("corrupt learning store")

// Store manages the learning JSONL file and tag index.
type Store struct {
	Dir              string               // .attest/learnings/
	Now              func() time.Time     // clock injection for tests; defaults to time.Now
	OnMaintain       func(MaintainReport) // optional callback for logging
	mu               sync.Mutex
	lastMaintainedAt time.Time
	maintaining      atomic.Bool
	bgWg             sync.WaitGroup // tracks background maintenance goroutines
}

// NewStore creates a learning store for the given directory.
func NewStore(dir string) *Store {
	return &Store{Dir: dir, Now: time.Now}
}

// Add appends a learning to the store. Acquires exclusive lock.
func (s *Store) Add(l *Learning) error {
	isNew := l.ID == ""
	if isNew {
		id, err := generateID()
		if err != nil {
			return err
		}
		l.ID = id
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = s.now()
	}
	if isNew && l.Confidence == 0 {
		l.Confidence = 0.5
	}
	if isNew && l.Utility == 0 {
		l.Utility = 0.5
	}
	// Validate category.
	switch l.Category {
	case CategoryPattern, CategoryAntiPattern, CategoryTooling, CategoryCodebase, CategoryProcess:
		// valid
	case "":
		l.Category = CategoryCodebase
	default:
		return fmt.Errorf("unknown category %q (valid: pattern, anti_pattern, tooling, codebase, process)", l.Category)
	}

	// Cap content length.
	const maxContentLen = 10240 // 10KB
	if len(l.Content) > maxContentLen {
		l.Content = l.Content[:maxContentLen]
	}

	if l.Source == "" {
		l.Source = "manual"
	}
	if l.Maturity == "" {
		l.Maturity = MaturityProvisional
	} else {
		switch l.Maturity {
		case MaturityProvisional, MaturityCandidate, MaturityEstablished:
			// valid
		default:
			return fmt.Errorf("unknown maturity %q (valid: provisional, candidate, established)", l.Maturity)
		}
	}

	// Normalize tags.
	for i := range l.Tags {
		l.Tags[i] = strings.ToLower(strings.TrimSpace(l.Tags[i]))
	}

	// Normalize SourcePaths: filepath.Clean + forward slashes.
	for i := range l.SourcePaths {
		l.SourcePaths[i] = normalizePath(l.SourcePaths[i])
	}

	return s.withLock(func() error {
		learnings, skipped, err := s.readAllWithCount()
		if err != nil {
			return err
		}
		if skipped > 0 {
			return fmt.Errorf("%w: %d corrupt lines — run 'attest learn repair' to fix",
				ErrCorruptLearningStore, skipped)
		}
		learnings = append(learnings, *l)
		if err := s.writeAll(learnings); err != nil {
			return err
		}
		return s.rebuildIndex(learnings)
	})
}

// RecordCitation increments CitedCount and sets LastCitedAt. Acquires lock.
func (s *Store) RecordCitation(id string) error {
	return s.RecordCitations([]string{id})
}

// RecordCitations increments CitedCount and sets LastCitedAt for multiple learnings
// in a single store write. Acquires lock once.
func (s *Store) RecordCitations(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.withLock(func() error {
		learnings, skipped, err := s.readAllWithCount()
		if err != nil {
			return err
		}
		if skipped > 0 {
			return fmt.Errorf("%w: %d corrupt lines — run 'attest learn repair' to fix",
				ErrCorruptLearningStore, skipped)
		}
		idSet := make(map[string]bool, len(ids))
		for _, id := range ids {
			idSet[id] = true
		}
		now := s.now()
		found := 0
		for i := range learnings {
			if idSet[learnings[i].ID] {
				learnings[i].CitedCount++
				learnings[i].LastCitedAt = &now
				found++
				if found == len(idSet) {
					break
				}
			}
		}
		if found == 0 {
			return fmt.Errorf("no learnings found for citation IDs")
		}
		return s.writeAll(learnings)
	})
}

func maturityWeight(m Maturity) float64 {
	switch m {
	case MaturityEstablished:
		return 1.5
	case MaturityCandidate:
		return 1.2
	case MaturityProvisional, "":
		return 1.0
	default:
		return 1.0
	}
}

// Query returns learnings matching the filter options.
// Applies lazy utility decay at query time.
func (s *Store) Query(opts QueryOpts) ([]Learning, error) {
	s.MaintainIfStale()
	learnings, err := s.readAll()
	if err != nil {
		return nil, err
	}

	now := s.now()
	var results []Learning
	for i := range learnings {
		l := &learnings[i]
		if l.Expired || l.SupersededBy != "" {
			continue
		}

		// Apply lazy decay.
		refTime := l.CreatedAt
		if l.LastCitedAt != nil {
			refTime = *l.LastCitedAt
		}
		if now.Sub(refTime) > decayThreshold {
			l.Utility -= decayAmount
			if l.Utility < 0 {
				l.Utility = 0
			}
		}

		if !s.matchesFilter(l, &opts) {
			continue
		}
		results = append(results, *l)
	}

	// Sort.
	switch opts.SortBy {
	case "created_at":
		sort.Slice(results, func(i, j int) bool {
			return results[i].CreatedAt.After(results[j].CreatedAt)
		})
	default: // "utility" — weighted by maturity
		sort.Slice(results, func(i, j int) bool {
			scoreI := results[i].Utility * maturityWeight(results[i].Maturity)
			scoreJ := results[j].Utility * maturityWeight(results[j].Maturity)
			if scoreI != scoreJ {
				return scoreI > scoreJ
			}
			// Tie-break: higher raw utility first, then newer, then ID
			if results[i].Utility != results[j].Utility {
				return results[i].Utility > results[j].Utility
			}
			if !results[i].CreatedAt.Equal(results[j].CreatedAt) {
				return results[i].CreatedAt.After(results[j].CreatedAt)
			}
			return results[i].ID < results[j].ID
		})
	}

	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	return results, nil
}

// QueryLearnings satisfies state.LearningEnricher. Returns top learnings
// matching the given tags or paths, sorted by utility.
func (s *Store) QueryLearnings(opts state.LearningQueryOpts) ([]state.LearningRef, error) {
	results, err := s.Query(QueryOpts{
		Tags:       opts.Tags,
		Paths:      opts.Paths,
		MinUtility: opts.MinUtility,
		Limit:      opts.Limit,
		SortBy:     "utility",
	})
	if err != nil {
		return nil, err
	}
	refs := make([]state.LearningRef, len(results))
	for i := range results {
		refs[i] = state.LearningRef{
			ID:       results[i].ID,
			Category: string(results[i].Category),
			Utility:  results[i].Utility,
			Summary:  results[i].Summary,
			Maturity: string(results[i].Maturity),
		}
	}
	return refs, nil
}

// Get returns a single learning by ID.
func (s *Store) Get(id string) (*Learning, error) {
	learnings, err := s.readAll()
	if err != nil {
		return nil, err
	}
	for i := range learnings {
		if learnings[i].ID == id {
			return &learnings[i], nil
		}
	}
	return nil, fmt.Errorf("learning %s not found", id)
}

// RebuildIndex rebuilds the tag inverted index from the JSONL store.
func (s *Store) RebuildIndex() error {
	return s.withLock(func() error {
		learnings, err := s.readAll()
		if err != nil {
			return err
		}
		return s.rebuildIndex(learnings)
	})
}

// WriteHandoff writes a session handoff artifact.
func (s *Store) WriteHandoff(h *SessionHandoff) error {
	if h.ID == "" {
		id, err := generateID()
		if err != nil {
			return err
		}
		h.ID = "hnd-" + id[4:] // reuse the random part
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = s.now()
	}

	handoffsDir := filepath.Join(s.Dir, "handoffs")
	if err := os.MkdirAll(handoffsDir, 0o755); err != nil {
		return fmt.Errorf("create handoffs dir: %w", err)
	}

	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal handoff: %w", err)
	}

	// Write timestamped file.
	ts := h.CreatedAt.UTC().Format("2006-01-02T150405Z")
	path := filepath.Join(handoffsDir, ts+".json")
	if err := atomicWrite(path, data); err != nil {
		return err
	}

	// Copy to latest.
	latestPath := filepath.Join(s.Dir, "latest-handoff.json")
	return atomicWrite(latestPath, data)
}

// LatestHandoff returns the most recent handoff, or nil if none exists.
func (s *Store) LatestHandoff() (*SessionHandoff, error) {
	path := filepath.Join(s.Dir, "latest-handoff.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil //nolint:nilnil // nil,nil means "no handoff exists" — not an error
		}
		return nil, err
	}
	var h SessionHandoff
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("parse handoff: %w", err)
	}
	return &h, nil
}

// AssembleContext builds a ContextBundle for a task by querying learnings
// matching the task's tags and paths, deduped and capped at token budget.
func (s *Store) AssembleContext(taskID string, tags, paths []string) (*ContextBundle, error) {
	const tokenBudget = 2000
	const maxLearnings = 8

	results, err := s.Query(QueryOpts{
		Tags:       tags,
		Paths:      paths,
		MinUtility: 0.3,
		SortBy:     "utility",
	})
	if err != nil {
		return nil, err
	}

	// Cap at token budget and max learnings.
	var selected []Learning
	tokensUsed := 0
	for i := range results {
		tokens := len(results[i].Content) / 4
		if tokensUsed+tokens > tokenBudget {
			break
		}
		selected = append(selected, results[i])
		tokensUsed += tokens
		if len(selected) >= maxLearnings {
			break
		}
	}

	// Record citations for all selected learnings (best-effort, single write).
	if len(selected) > 0 {
		ids := make([]string, len(selected))
		for i := range selected {
			ids[i] = selected[i].ID
		}
		_ = s.RecordCitations(ids)
	}

	handoff, _ := s.LatestHandoff()
	// Only include handoff if < 24h old.
	if handoff != nil && s.now().Sub(handoff.CreatedAt) >= 24*time.Hour {
		handoff = nil
	}

	return &ContextBundle{
		TaskID:      taskID,
		Learnings:   selected,
		Handoff:     handoff,
		TokensUsed:  tokensUsed,
		TokenBudget: tokenBudget,
	}, nil
}

// Contradiction records two learnings with conflicting categories on overlapping tags.
type Contradiction struct {
	LearningA  string
	LearningB  string
	SharedTags []string
}

// MaintainReport summarizes maintenance actions taken.
type MaintainReport struct {
	Merged         int
	Contradictions []Contradiction
	Promoted       int
	Demoted        int
	Stale          int
	GCRemoved      int
	IndexRebuilt   bool
	Skipped        bool
}

// readAllWithCount reads all learnings and returns the count of skipped corrupt lines.
func (s *Store) readAllWithCount() ([]Learning, int, error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, 0, fmt.Errorf("create learnings dir: %w", err)
	}
	path := filepath.Join(s.Dir, "index.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("read index: %w", err)
	}

	var learnings []Learning
	var skipped int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var l Learning
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			skipped++
			continue
		}
		learnings = append(learnings, l)
	}
	return learnings, skipped, nil
}

// GarbageCollect removes expired and superseded learnings older than maxAge.
// Delegates to Maintain for the actual work.
func (s *Store) GarbageCollect(maxAge time.Duration) (int, error) {
	report, err := s.Maintain(maxAge)
	if err != nil {
		return 0, err
	}
	return report.GCRemoved, nil
}

// Maintain runs knowledge store maintenance: maturity transitions,
// staleness scan, index rebuild, and garbage collection.
// Returns ErrCorruptLearningStore if index.jsonl has corrupt lines.
func (s *Store) Maintain(maxAge time.Duration) (*MaintainReport, error) {
	if !s.maintaining.CompareAndSwap(false, true) {
		return &MaintainReport{Skipped: true}, nil
	}
	defer s.maintaining.Store(false)

	report := &MaintainReport{}
	err := s.withLock(func() error {
		learnings, skipped, readErr := s.readAllWithCount()
		if readErr != nil {
			return readErr
		}
		if skipped > 0 {
			return fmt.Errorf("%w: %d corrupt lines — run 'attest learn repair' to fix",
				ErrCorruptLearningStore, skipped)
		}

		applyDedup(learnings, report)
		report.Contradictions = findContradictions(learnings)
		applyMaturityTransitions(learnings, report)
		applyStalenessDecay(learnings, s.now(), report)
		kept := applyGC(learnings, s.now(), maxAge, report)

		// 4. Write + rebuild index
		if writeErr := s.writeAll(kept); writeErr != nil {
			return writeErr
		}
		report.IndexRebuilt = true
		return s.rebuildIndex(kept)
	})

	s.mu.Lock()
	s.lastMaintainedAt = s.now()
	s.mu.Unlock()
	return report, err
}

// Repair drops corrupt JSONL lines and rewrites the store.
// Returns (kept, dropped, error). Backs up the corrupt file first.
func (s *Store) Repair() (kept, dropped int, retErr error) {
	retErr = s.withLock(func() error {
		learnings, skipped, readErr := s.readAllWithCount()
		if readErr != nil {
			return readErr
		}
		if skipped == 0 {
			kept = len(learnings)
			return nil // nothing to repair
		}

		// Back up corrupt file.
		src := filepath.Join(s.Dir, "index.jsonl")
		backup := src + ".corrupt." + s.now().Format("20060102T150405")
		data, err := os.ReadFile(src)
		if err == nil {
			_ = os.WriteFile(backup, data, 0o644) //nolint:gosec // backup path derived from store dir, not user input
		}

		// Rewrite with only valid entries.
		if writeErr := s.writeAll(learnings); writeErr != nil {
			return writeErr
		}
		if idxErr := s.rebuildIndex(learnings); idxErr != nil {
			return idxErr
		}
		kept = len(learnings)
		dropped = skipped
		return nil
	})
	return kept, dropped, retErr
}

// MaintainIfStale triggers background maintenance if more than 24 hours have passed
// since the last maintenance run, or if maintenance has never run. Non-blocking — spawns a goroutine.
func (s *Store) MaintainIfStale() {
	s.mu.Lock()
	stale := s.lastMaintainedAt.IsZero() || s.now().Sub(s.lastMaintainedAt) > 24*time.Hour
	if stale {
		// Mark as maintained now to prevent concurrent re-triggers.
		s.lastMaintainedAt = s.now()
	}
	s.mu.Unlock()
	if stale {
		s.bgWg.Add(1)
		go func() {
			defer s.bgWg.Done()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "warning: background maintenance panicked: %v\n", r)
				}
			}()
			report, _ := s.Maintain(90 * 24 * time.Hour)
			if report != nil && !report.Skipped && s.OnMaintain != nil {
				s.OnMaintain(*report)
			}
		}()
	}
}

// Wait waits for any background maintenance goroutines to complete.
func (s *Store) Wait() {
	s.bgWg.Wait()
}

// checkMaturityTransition returns the new maturity level if a transition is warranted.
// applyDedup merges learnings with ≥2 shared tags and similar summaries.
func applyDedup(learnings []Learning, report *MaintainReport) {
	for i := range learnings {
		if learnings[i].Expired || learnings[i].SupersededBy != "" {
			continue
		}
		for j := i + 1; j < len(learnings); j++ {
			if learnings[j].Expired || learnings[j].SupersededBy != "" {
				continue
			}
			shared := sharedTagCount(learnings[i].Tags, learnings[j].Tags)
			if shared >= 2 && similarSummary(learnings[i].Summary, learnings[j].Summary) {
				// Keep the one with higher utility.
				if learnings[i].Utility >= learnings[j].Utility {
					learnings[j].SupersededBy = learnings[i].ID
				} else {
					learnings[i].SupersededBy = learnings[j].ID
				}
				report.Merged++
			}
		}
	}
}

func sharedTagCount(a, b []string) int {
	set := make(map[string]bool, len(a))
	for _, t := range a {
		set[t] = true
	}
	count := 0
	for _, t := range b {
		if set[t] {
			count++
		}
	}
	return count
}

func similarSummary(a, b string) bool {
	// Simple: check if shorter is a substring of longer, or they share >80% words.
	if a == b {
		return true
	}
	shorter, longer := a, b
	if len(a) > len(b) {
		shorter, longer = b, a
	}
	if shorter == "" {
		return false
	}
	return strings.Contains(strings.ToLower(longer), strings.ToLower(shorter))
}

// findContradictions finds anti_pattern + pattern learnings with ≥2 shared tags.
func findContradictions(learnings []Learning) []Contradiction {
	var result []Contradiction
	for i := range learnings {
		if learnings[i].Expired || learnings[i].SupersededBy != "" {
			continue
		}
		for j := i + 1; j < len(learnings); j++ {
			if learnings[j].Expired || learnings[j].SupersededBy != "" {
				continue
			}
			// One must be anti_pattern, other must be pattern.
			isConflict := (learnings[i].Category == CategoryAntiPattern && learnings[j].Category == CategoryPattern) ||
				(learnings[i].Category == CategoryPattern && learnings[j].Category == CategoryAntiPattern)
			if !isConflict {
				continue
			}
			shared := sharedTags(learnings[i].Tags, learnings[j].Tags)
			if len(shared) >= 2 {
				result = append(result, Contradiction{
					LearningA:  learnings[i].ID,
					LearningB:  learnings[j].ID,
					SharedTags: shared,
				})
			}
		}
	}
	return result
}

func sharedTags(a, b []string) []string {
	set := make(map[string]bool, len(a))
	for _, t := range a {
		set[t] = true
	}
	var shared []string
	for _, t := range b {
		if set[t] {
			shared = append(shared, t)
		}
	}
	return shared
}

func checkMaturityTransition(l *Learning) Maturity {
	switch l.Maturity {
	case MaturityProvisional, "":
		if l.CitedCount >= 3 && l.Utility >= 0.55 {
			return MaturityCandidate
		}
	case MaturityCandidate:
		if l.CitedCount >= 5 && l.Utility >= 0.55 {
			return MaturityEstablished
		}
		if l.Utility < 0.3 {
			return MaturityProvisional
		}
	}
	return ""
}

func applyMaturityTransitions(learnings []Learning, report *MaintainReport) {
	for i := range learnings {
		l := &learnings[i]
		if l.Expired || l.SupersededBy != "" {
			continue
		}
		newMaturity := checkMaturityTransition(l)
		if newMaturity == "" || newMaturity == l.Maturity {
			continue
		}
		if newMaturity == MaturityCandidate || newMaturity == MaturityEstablished {
			report.Promoted++
		} else {
			report.Demoted++
		}
		l.Maturity = newMaturity
	}
}

func applyStalenessDecay(learnings []Learning, now time.Time, report *MaintainReport) {
	for i := range learnings {
		l := &learnings[i]
		if l.Expired || l.SupersededBy != "" {
			continue
		}
		if l.CitedCount == 0 && now.Sub(l.CreatedAt) > 30*24*time.Hour {
			l.Utility = max(l.Utility-0.1, 0)
			report.Stale++
		}
	}
}

func applyGC(learnings []Learning, now time.Time, maxAge time.Duration, report *MaintainReport) []Learning {
	kept := make([]Learning, 0, len(learnings))
	for i := range learnings {
		l := &learnings[i]
		if (l.Expired || l.SupersededBy != "") && now.Sub(l.CreatedAt) > maxAge {
			report.GCRemoved++
			continue
		}
		kept = append(kept, *l)
	}
	return kept
}

// --- Internal helpers ---

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) matchesFilter(l *Learning, opts *QueryOpts) bool {
	if opts.MinUtility > 0 && l.Utility < opts.MinUtility {
		return false
	}
	if opts.Category != "" && l.Category != opts.Category {
		return false
	}
	hasTags := len(opts.Tags) > 0
	hasPaths := len(opts.Paths) > 0
	if hasTags || hasPaths {
		tagMatch := hasTags && (matchesAnyTag(l.Tags, opts.Tags) || matchesAnyKeyword(l.Content, opts.Tags))
		pathMatch := hasPaths && matchesAnyPath(l.SourcePaths, opts.Paths)
		if !tagMatch && !pathMatch {
			return false
		}
	}
	return true
}

func matchesAnyTag(learningTags, queryTags []string) bool {
	for _, qt := range queryTags {
		for _, lt := range learningTags {
			if strings.EqualFold(qt, lt) {
				return true
			}
		}
	}
	return false
}

func matchesAnyPath(sourcePaths, queryPaths []string) bool {
	for _, qp := range queryPaths {
		nqp := normalizePath(qp)
		for _, lp := range sourcePaths {
			nlp := normalizePath(lp)
			if nqp == nlp {
				return true
			}
			// Handle absolute/relative mismatch: check suffix containment.
			if strings.HasSuffix(nqp, "/"+nlp) || strings.HasSuffix(nlp, "/"+nqp) {
				return true
			}
		}
	}
	return false
}

// normalizePath cleans a path and converts to forward slashes.
func normalizePath(p string) string {
	p = filepath.Clean(p)
	p = filepath.ToSlash(p)
	// Strip leading ./ if present.
	p = strings.TrimPrefix(p, "./")
	return p
}

func (s *Store) readAll() ([]Learning, error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create learnings dir: %w", err)
	}
	path := filepath.Join(s.Dir, "index.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read index: %w", err)
	}

	var learnings []Learning
	var skipped int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var l Learning
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			skipped++
			continue
		}
		learnings = append(learnings, l)
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "warning: skipped %d corrupt lines in %s\n", skipped, path)
	}
	return learnings, nil
}

func (s *Store) writeAll(learnings []Learning) error {
	var buf strings.Builder
	for i := range learnings {
		data, err := json.Marshal(&learnings[i])
		if err != nil {
			return fmt.Errorf("marshal learning %s: %w", learnings[i].ID, err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	path := filepath.Join(s.Dir, "index.jsonl")
	return atomicWrite(path, []byte(buf.String()))
}

func (s *Store) withLock(fn func() error) error {
	lockPath := filepath.Join(s.Dir, ".lock")
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("create learnings dir: %w", err)
	}
	fl := flock.New(lockPath)
	if err := fl.Lock(); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer func() { _ = fl.Unlock() }()
	return fn()
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".attest-learning-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func generateID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "lrn-" + hex.EncodeToString(b), nil
}
