package learning

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/runger/attest/internal/state"
)

// DefaultLeaseDuration is unused here but exported for consistency.
const (
	decayThreshold = 30 * 24 * time.Hour // 30 days
	decayAmount    = 0.05
)

// Store manages the learning JSONL file and tag index.
type Store struct {
	Dir string           // .attest/learnings/
	Now func() time.Time // clock injection for tests; defaults to time.Now
}

// NewStore creates a learning store for the given directory.
func NewStore(dir string) *Store {
	return &Store{Dir: dir, Now: time.Now}
}

// Add appends a learning to the store. Acquires exclusive lock.
func (s *Store) Add(l *Learning) error {
	if l.ID == "" {
		id, err := generateID()
		if err != nil {
			return err
		}
		l.ID = id
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = s.now()
	}
	if l.Confidence == 0 {
		l.Confidence = 0.5
	}
	if l.Utility == 0 {
		l.Utility = 0.5
	}
	// Normalize tags.
	for i := range l.Tags {
		l.Tags[i] = strings.ToLower(strings.TrimSpace(l.Tags[i]))
	}

	return s.withLock(func() error {
		learnings, err := s.readAll()
		if err != nil {
			return err
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
	return s.withLock(func() error {
		learnings, err := s.readAll()
		if err != nil {
			return err
		}
		now := s.now()
		for i := range learnings {
			if learnings[i].ID == id {
				learnings[i].CitedCount++
				learnings[i].LastCitedAt = &now
				return s.writeAll(learnings)
			}
		}
		return fmt.Errorf("learning %s not found", id)
	})
}

// Query returns learnings matching the filter options.
// Applies lazy utility decay at query time.
func (s *Store) Query(opts QueryOpts) ([]Learning, error) {
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
	default: // "utility"
		sort.Slice(results, func(i, j int) bool {
			return results[i].Utility > results[j].Utility
		})
	}

	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	return results, nil
}

// QueryByTagsAndPaths satisfies state.LearningEnricher. Returns top learnings
// matching the given tags or paths, sorted by utility.
func (s *Store) QueryByTagsAndPaths(tags, paths []string, limit int) ([]state.LearningRef, error) {
	results, err := s.Query(QueryOpts{
		Tags:       tags,
		Paths:      paths,
		MinUtility: 0.1,
		Limit:      limit,
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

// GarbageCollect removes expired and superseded learnings older than maxAge.
func (s *Store) GarbageCollect(maxAge time.Duration) (int, error) {
	var removed int
	err := s.withLock(func() error {
		learnings, readErr := s.readAll()
		if readErr != nil {
			return readErr
		}

		now := s.now()
		var kept []Learning
		for i := range learnings {
			l := &learnings[i]
			if (l.Expired || l.SupersededBy != "") && now.Sub(l.CreatedAt) > maxAge {
				removed++
				continue
			}
			kept = append(kept, *l)
		}

		if err := s.writeAll(kept); err != nil {
			return err
		}
		return s.rebuildIndex(kept)
	})
	return removed, err
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
	if len(opts.Tags) > 0 && !matchesAnyTag(l.Tags, opts.Tags) {
		return false
	}
	if len(opts.Paths) > 0 && !matchesAnyPath(l.SourcePaths, opts.Paths) {
		return false
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
		for _, lp := range sourcePaths {
			if qp == lp {
				return true
			}
		}
	}
	return false
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
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var l Learning
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			continue // skip corrupt lines
		}
		learnings = append(learnings, l)
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

func (s *Store) rebuildIndex(learnings []Learning) error {
	idx := TagIndex{Tags: make(map[string][]string)}
	for i := range learnings {
		if learnings[i].Expired || learnings[i].SupersededBy != "" {
			continue
		}
		for _, tag := range learnings[i].Tags {
			idx.Tags[tag] = append(idx.Tags[tag], learnings[i].ID)
		}
	}
	idx.Version++

	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	return atomicWrite(filepath.Join(s.Dir, "tags.json"), data)
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
