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
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"

	"github.com/php-workx/fabrikk/internal/state"
)

// autoExpiryThreshold is the duration after which a learning with no query match is expired.
const autoExpiryThreshold = 90 * 24 * time.Hour

// indexFile is the JSONL filename used for the learning store index.
const indexFile = "index.jsonl"

// corruptLinesFmt is the error format for corrupt JSONL lines.
const corruptLinesFmt = "%w: %d corrupt lines — run 'fabrikk learn repair' to fix"

// ErrCorruptLearningStore indicates the JSONL store has corrupt lines.
var ErrCorruptLearningStore = errors.New("corrupt learning store")

// Store manages the learning JSONL file and tag index.
type Store struct {
	SharedDir  string               // committed path: .fabrikk/learnings/
	LocalDir   string               // local-only path: .git/fabrikk/learnings/ (optional)
	Scanner    *ContentScanner      // content scanner (nil = no scanning)
	Now        func() time.Time     // clock injection for tests; defaults to time.Now
	OnMaintain func(MaintainReport) // optional callback for logging

	maintaining atomic.Bool
}

// NewStore creates a learning store with a single directory (backward-compatible).
func NewStore(dir string) *Store {
	return &Store{SharedDir: dir, Now: time.Now}
}

// NewStoreWithLocalDir creates a split store with local (unredacted) and shared (redacted) directories.
// Content scanning is enabled automatically using custom terms from {sharedDir}/../scan-terms.txt.
func NewStoreWithLocalDir(sharedDir, localDir string) *Store {
	scanTermsPath := filepath.Join(filepath.Dir(sharedDir), "scan-terms.txt")
	return &Store{
		SharedDir: sharedDir,
		LocalDir:  localDir,
		Scanner:   NewContentScanner(scanTermsPath),
		Now:       time.Now,
	}
}

// Add appends a learning to the store. Acquires exclusive lock.
func (s *Store) Add(l *Learning) error {
	if err := s.initLearningDefaults(l); err != nil {
		return err
	}
	normalizeLearningFields(l)

	return s.withLock(func() error {
		learnings, skipped, err := s.readAllWithCount()
		if err != nil {
			return err
		}
		if skipped > 0 {
			return fmt.Errorf(corruptLinesFmt,
				ErrCorruptLearningStore, skipped)
		}
		learnings = append(learnings, *l)
		if err := s.writeAll(learnings); err != nil {
			return err
		}
		return s.rebuildIndex(learnings)
	})
}

// initLearningDefaults sets default ID, timestamps, confidence, category, and source.
func (s *Store) initLearningDefaults(l *Learning) error {
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
	if err := validateCategory(&l.Category); err != nil {
		return err
	}
	if l.Source == "" {
		l.Source = "manual"
	}
	return nil
}

// validateCategory normalizes an empty category to CategoryCodebase or rejects unknown values.
func validateCategory(cat *Category) error {
	switch *cat {
	case CategoryPattern, CategoryAntiPattern, CategoryTooling, CategoryCodebase, CategoryProcess:
		return nil
	case "":
		*cat = CategoryCodebase
		return nil
	default:
		return fmt.Errorf("unknown category %q (valid: pattern, anti_pattern, tooling, codebase, process)", *cat)
	}
}

// normalizeLearningFields caps content length and normalizes tags and paths.
func normalizeLearningFields(l *Learning) {
	const maxContentLen = 10240 // 10KB
	if len(l.Content) > maxContentLen {
		l.Content = l.Content[:maxContentLen]
	}
	for i := range l.Tags {
		l.Tags[i] = strings.ToLower(strings.TrimSpace(l.Tags[i]))
	}
	for i := range l.SourcePaths {
		l.SourcePaths[i] = normalizePath(l.SourcePaths[i])
	}
}

// RecordOutcome records verification outcomes for learnings attached to a task.
// Increments AttachCount for all IDs, and SuccessCount when passed is true.
func (s *Store) RecordOutcome(ids []string, passed bool) error {
	if len(ids) == 0 {
		return nil
	}
	return s.withLock(func() error {
		learnings, skipped, err := s.readAllWithCount()
		if err != nil {
			return err
		}
		if skipped > 0 {
			return fmt.Errorf(corruptLinesFmt,
				ErrCorruptLearningStore, skipped)
		}
		applyOutcomes(learnings, ids, passed, s.now())
		return s.writeAll(learnings)
	})
}

// applyOutcomes increments attach/success counters for matching learnings.
func applyOutcomes(learnings []Learning, ids []string, passed bool, now time.Time) {
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	for i := range learnings {
		if !idSet[learnings[i].ID] {
			continue
		}
		learnings[i].AttachCount++
		if passed {
			learnings[i].SuccessCount++
		}
		learnings[i].LastAttachedAt = &now
	}
}

// Query returns learnings matching the filter options.
func (s *Store) Query(opts QueryOpts) ([]Learning, error) {
	learnings, err := s.readAll()
	if err != nil {
		return nil, err
	}

	results := s.filterActiveLearnings(learnings, &opts)
	sortLearnings(results, opts.SortBy)

	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	return results, nil
}

// filterActiveLearnings returns non-expired, non-superseded learnings that match the filter.
func (s *Store) filterActiveLearnings(learnings []Learning, opts *QueryOpts) []Learning {
	var results []Learning
	for i := range learnings {
		l := &learnings[i]
		if l.Expired || l.SupersededBy != "" {
			continue
		}
		if !s.matchesFilter(l, opts) {
			continue
		}
		results = append(results, *l)
	}
	return results
}

// sortLearnings sorts by the given sort key (default: effectiveness, then created_at, then ID).
func sortLearnings(results []Learning, sortBy string) {
	switch sortBy {
	case "created_at":
		sort.Slice(results, func(i, j int) bool {
			return results[i].CreatedAt.After(results[j].CreatedAt)
		})
	default: // "effectiveness"
		sort.Slice(results, func(i, j int) bool {
			return lessEffectiveness(&results[i], &results[j])
		})
	}
}

// lessEffectiveness returns true if a should sort before b by effectiveness, then recency, then ID.
func lessEffectiveness(a, b *Learning) bool {
	effA, effB := a.Effectiveness(), b.Effectiveness()
	if effA != effB {
		return effA > effB
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	return a.ID < b.ID
}

// QueryLearnings satisfies state.LearningEnricher. Returns top learnings
// matching the given tags or paths, sorted by effectiveness.
func (s *Store) QueryLearnings(opts state.LearningQueryOpts) ([]state.LearningRef, error) {
	results, err := s.Query(QueryOpts{
		Tags:             opts.Tags,
		Paths:            opts.Paths,
		SearchText:       opts.SearchText,
		MinEffectiveness: opts.MinEffectiveness,
		Limit:            opts.Limit,
		SortBy:           "effectiveness",
	})
	if err != nil {
		return nil, err
	}
	refs := make([]state.LearningRef, len(results))
	for i := range results {
		refs[i] = state.LearningRef{
			ID:       results[i].ID,
			Category: string(results[i].Category),
			Summary:  results[i].Summary,
		}
	}
	return refs, nil
}

// StoreHealth summarizes the learning store state.
type StoreHealth struct {
	Total       int
	Active      int
	Expired     int
	Superseded  int
	ByCategory  map[Category]int
	WithOutcome int // learnings with AttachCount > 0
	AvgEff      float64
}

// Health returns a summary of the learning store state.
func (s *Store) Health() (*StoreHealth, error) {
	learnings, err := s.readAll()
	if err != nil {
		return nil, err
	}
	h := &StoreHealth{
		Total:      len(learnings),
		ByCategory: make(map[Category]int),
	}
	var effSum float64
	for i := range learnings {
		l := &learnings[i]
		switch {
		case l.Expired:
			h.Expired++
		case l.SupersededBy != "":
			h.Superseded++
		default:
			h.Active++
			h.ByCategory[l.Category]++
			if l.AttachCount > 0 {
				h.WithOutcome++
				effSum += l.Effectiveness()
			}
		}
	}
	if h.WithOutcome > 0 {
		h.AvgEff = effSum / float64(h.WithOutcome)
	}
	return h, nil
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

	handoffsDir := filepath.Join(s.SharedDir, "handoffs")
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
	latestPath := filepath.Join(s.SharedDir, "latest-handoff.json")
	return atomicWrite(latestPath, data)
}

// LatestHandoff returns the most recent handoff, or nil if none exists.
func (s *Store) LatestHandoff() (*SessionHandoff, error) {
	path := filepath.Join(s.SharedDir, "latest-handoff.json")
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
// matching the task's tags, paths, and search text, capped at token budget.
func (s *Store) AssembleContext(taskID string, tags, paths []string, searchText string) (*ContextBundle, error) {
	const tokenBudget = 2000
	const maxLearnings = 8

	results, err := s.Query(QueryOpts{
		Tags:             tags,
		Paths:            paths,
		SearchText:       searchText,
		MinEffectiveness: 0.3,
		SortBy:           "effectiveness",
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

	// Load prevention checks matching the query paths, counting against token budget.
	queryPaths := paths
	if len(queryPaths) == 0 {
		queryPaths = tags
	}
	preventionChecks, tokensUsed := s.loadPreventionForContext(queryPaths, tokensUsed, tokenBudget)

	handoff, _ := s.LatestHandoff()
	// Only include handoff if < 24h old and fits in budget.
	if handoff != nil && s.now().Sub(handoff.CreatedAt) >= 24*time.Hour {
		handoff = nil
	}
	if handoff != nil {
		handoffTokens := len(handoff.Summary) / 4
		if tokensUsed+handoffTokens > tokenBudget {
			handoff = nil
		} else {
			tokensUsed += handoffTokens
		}
	}

	return &ContextBundle{
		TaskID:           taskID,
		Learnings:        selected,
		PreventionChecks: preventionChecks,
		Handoff:          handoff,
		TokensUsed:       tokensUsed,
		TokenBudget:      tokenBudget,
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
	Merged             int
	Contradictions     []Contradiction
	AutoExpired        int
	GCRemoved          int
	PreventionCompiled int
	IndexRebuilt       bool
	Skipped            bool
}

// readAllWithCount reads all learnings and returns the count of skipped corrupt lines.
// When LocalDir is set, merges both stores (LocalDir wins by ID for content).
func (s *Store) readAllWithCount() ([]Learning, int, error) {
	shared, sharedSkipped, err := readJSONLWithCount(s.SharedDir)
	if err != nil {
		return nil, 0, err
	}
	if s.LocalDir == "" {
		return shared, sharedSkipped, nil
	}
	local, localSkipped, localErr := readJSONLWithCount(s.LocalDir)
	if localErr != nil {
		//nolint:nilerr // LocalDir read failure is non-fatal — fall back to shared only
		return shared, sharedSkipped, nil
	}
	merged := mergeLearnings(local, shared)
	return merged, sharedSkipped + localSkipped, nil
}

// readJSONLWithCount reads learnings from a single directory's index.jsonl.
func readJSONLWithCount(dir string) ([]Learning, int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, 0, fmt.Errorf("create learnings dir: %w", err)
	}
	path := filepath.Join(dir, indexFile)
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
		if jsonErr := json.Unmarshal([]byte(line), &l); jsonErr != nil {
			skipped++
			continue
		}
		learnings = append(learnings, l)
	}
	return learnings, skipped, nil
}

// mergeLearnings merges two learning slices by ID. Primary (local) entries
// take precedence over secondary (shared) entries for the same ID.
func mergeLearnings(primary, secondary []Learning) []Learning {
	seen := make(map[string]bool, len(primary))
	merged := make([]Learning, 0, len(primary)+len(secondary))
	for i := range primary {
		seen[primary[i].ID] = true
		merged = append(merged, primary[i])
	}
	for i := range secondary {
		if !seen[secondary[i].ID] {
			merged = append(merged, secondary[i])
		}
	}
	return merged
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

// Maintain runs knowledge store maintenance: dedup, contradiction scan,
// auto-expiry, index rebuild, and garbage collection.
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
			return fmt.Errorf(corruptLinesFmt,
				ErrCorruptLearningStore, skipped)
		}

		applyDedup(learnings, report)
		report.Contradictions = findContradictions(learnings)
		applyAutoExpiry(learnings, s.now(), report)
		kept := applyGC(learnings, s.now(), maxAge, report)

		if writeErr := s.writeAll(kept); writeErr != nil {
			return writeErr
		}
		report.IndexRebuilt = true
		if idxErr := s.rebuildIndex(kept); idxErr != nil {
			return idxErr
		}

		// Compile prevention checks from high-effectiveness learnings.
		compiled, compileErr := s.compilePreventionChecks(kept)
		if compileErr != nil {
			return compileErr
		}
		report.PreventionCompiled = compiled
		return nil
	})

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
		src := filepath.Join(s.SharedDir, indexFile)
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

// Prevention check thresholds.
const (
	preventionMinEffectiveness = 0.7
	preventionMinAttachCount   = 3
)

// compilePreventionChecks writes high-effectiveness learnings as prevention
// check markdown files to both review/ and planning/ subdirectories.
// Returns the number of checks compiled.
func (s *Store) compilePreventionChecks(learnings []Learning) (int, error) {
	subdirs := []string{"review", "planning"}
	for _, subdir := range subdirs {
		dir := filepath.Join(s.SharedDir, "prevention", subdir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, fmt.Errorf("create prevention %s dir: %w", subdir, err)
		}
	}

	wanted, compiled, err := s.writeQualifiedChecks(learnings, subdirs)
	if err != nil {
		return compiled, err
	}
	pruneStaleChecks(s.SharedDir, subdirs, wanted)
	return compiled, nil
}

// qualifiesForPrevention returns true if a learning should be compiled into a prevention check.
func qualifiesForPrevention(l *Learning) bool {
	if l.Expired || l.SupersededBy != "" {
		return false
	}
	return l.AttachCount >= preventionMinAttachCount && l.Effectiveness() >= preventionMinEffectiveness
}

// writeQualifiedChecks writes prevention check files for qualifying learnings.
// Returns the set of wanted IDs, count written, and any error.
func (s *Store) writeQualifiedChecks(learnings []Learning, subdirs []string) (wantedIDs map[string]bool, written int, err error) {
	wanted := make(map[string]bool)
	compiled := 0
	for i := range learnings {
		l := &learnings[i]
		if !qualifiesForPrevention(l) {
			continue
		}
		wanted[l.ID] = true
		content := formatPreventionCheck(l, s.now())
		for _, subdir := range subdirs {
			path := filepath.Join(s.SharedDir, "prevention", subdir, l.ID+".md")
			if err := atomicWrite(path, []byte(content)); err != nil {
				return wanted, compiled, fmt.Errorf("write prevention check %s/%s: %w", subdir, l.ID, err)
			}
		}
		compiled++
	}
	return wanted, compiled, nil
}

// pruneStaleChecks removes prevention check files whose IDs are not in the wanted set.
func pruneStaleChecks(sharedDir string, subdirs []string, wanted map[string]bool) {
	for _, subdir := range subdirs {
		dir := filepath.Join(sharedDir, "prevention", subdir)
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".md") {
				continue
			}
			id := strings.TrimSuffix(name, ".md")
			if !wanted[id] {
				_ = os.Remove(filepath.Join(dir, name))
			}
		}
	}
}

func formatPreventionCheck(l *Learning, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nid: %s\nsource_learning: %s\ncompiled_at: %s\n",
		l.ID, l.ID, now.UTC().Format(time.RFC3339))
	if len(l.SourcePaths) > 0 {
		b.WriteString("applicable_paths:\n")
		for _, p := range l.SourcePaths {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	b.WriteString("---\n")
	fmt.Fprintf(&b, "# Prevention: %s\n\n", l.Summary)
	fmt.Fprintf(&b, "- **Category:** %s\n", l.Category)
	fmt.Fprintf(&b, "- **Effectiveness:** %.0f%% (%d/%d tasks passed)\n",
		l.Effectiveness()*100, l.SuccessCount, l.AttachCount)
	if l.Content != "" {
		b.WriteString("\n")
		b.WriteString(l.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// LoadPreventionContext reads prevention check files matching the given paths
// and returns them as a formatted string for council reviewer context.
func (s *Store) LoadPreventionContext(queryPaths []string) string {
	preventDir := filepath.Join(s.SharedDir, "prevention", "review")
	entries, err := os.ReadDir(preventDir)
	if err != nil {
		return ""
	}

	var checks []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(preventDir, e.Name()))
		if readErr != nil {
			continue
		}
		content := string(data)

		// If query paths provided, only include checks with matching applicable_paths.
		if len(queryPaths) > 0 && !preventionMatchesPaths(content, queryPaths) {
			continue
		}
		checks = append(checks, content)
	}

	if len(checks) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Prior Findings (from learning store)\n\n")
	b.WriteString("The following issues have been identified in previous reviews. ")
	b.WriteString("Consider these when reviewing. Do not re-raise resolved issues ")
	b.WriteString("unless you see evidence they have regressed.\n\n")
	for _, check := range checks {
		b.WriteString(check)
		b.WriteString("\n---\n\n")
	}
	return b.String()
}

// loadPreventionForContext loads prevention checks matching paths, respecting token budget.
func (s *Store) loadPreventionForContext(queryPaths []string, tokensUsed, tokenBudget int) (checks []string, totalTokens int) {
	totalTokens = tokensUsed
	for _, subdir := range []string{"review", "planning"} {
		preventDir := filepath.Join(s.SharedDir, "prevention", subdir)
		var done bool
		checks, totalTokens, done = collectPreventionChecks(preventDir, queryPaths, checks, totalTokens, tokenBudget)
		if done {
			return checks, totalTokens
		}
	}
	return checks, totalTokens
}

// collectPreventionChecks reads matching prevention checks from a directory, appending to checks
// until the token budget is exceeded. Returns done=true when budget is exhausted.
func collectPreventionChecks(dir string, queryPaths, checks []string, tokensUsed, tokenBudget int) (updatedChecks []string, updatedTokens int, done bool) {
	entries, dirErr := os.ReadDir(dir)
	if dirErr != nil {
		return checks, tokensUsed, false
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			continue
		}
		content := string(data)
		if len(queryPaths) > 0 && !preventionMatchesPaths(content, queryPaths) {
			continue
		}
		contentTokens := len(content) / 4
		if tokensUsed+contentTokens > tokenBudget {
			return checks, tokensUsed, true
		}
		checks = append(checks, content)
		tokensUsed += contentTokens
	}
	return checks, tokensUsed, false
}

// preventionMatchesPaths checks if a prevention check file's applicable_paths
// overlap with any of the query paths.
func preventionMatchesPaths(content string, queryPaths []string) bool {
	for _, qp := range queryPaths {
		nqp := normalizePath(qp)
		if strings.Contains(content, nqp) {
			return true
		}
	}
	return false
}

// applyDedup merges learnings with ≥2 shared tags and similar summaries.
func applyDedup(learnings []Learning, report *MaintainReport) {
	for i := range learnings {
		if !isActiveLearning(&learnings[i]) {
			continue
		}
		for j := i + 1; j < len(learnings); j++ {
			if !isActiveLearning(&learnings[j]) {
				continue
			}
			if tryMergeDuplicate(&learnings[i], &learnings[j]) {
				report.Merged++
			}
		}
	}
}

// isActiveLearning returns true if the learning is neither expired nor superseded.
func isActiveLearning(l *Learning) bool {
	return !l.Expired && l.SupersededBy == ""
}

// tryMergeDuplicate checks if two learnings are duplicates (≥2 shared tags + similar summaries)
// and supersedes the weaker one. Returns true if a merge occurred.
func tryMergeDuplicate(a, b *Learning) bool {
	shared := sharedTagCount(a.Tags, b.Tags)
	if shared < 2 || !similarSummary(a.Summary, b.Summary) {
		return false
	}
	if a.Effectiveness() >= b.Effectiveness() {
		b.SupersededBy = a.ID
	} else {
		a.SupersededBy = b.ID
	}
	return true
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
		if !isActiveLearning(&learnings[i]) {
			continue
		}
		for j := i + 1; j < len(learnings); j++ {
			if !isActiveLearning(&learnings[j]) {
				continue
			}
			if c, ok := detectContradiction(&learnings[i], &learnings[j]); ok {
				result = append(result, c)
			}
		}
	}
	return result
}

// areCategoryConflict returns true if one learning is a pattern and the other is an anti-pattern.
func areCategoryConflict(a, b *Learning) bool {
	return (a.Category == CategoryAntiPattern && b.Category == CategoryPattern) ||
		(a.Category == CategoryPattern && b.Category == CategoryAntiPattern)
}

// detectContradiction checks if two learnings are contradictory (conflicting categories + ≥2 shared tags).
func detectContradiction(a, b *Learning) (Contradiction, bool) {
	if !areCategoryConflict(a, b) {
		return Contradiction{}, false
	}
	shared := sharedTags(a.Tags, b.Tags)
	if len(shared) < 2 {
		return Contradiction{}, false
	}
	return Contradiction{
		LearningA:  a.ID,
		LearningB:  b.ID,
		SharedTags: shared,
	}, true
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

// applyAutoExpiry marks learnings as expired if they haven't been attached to
// a task in 90 days. Uses CreatedAt as fallback when never attached.
func applyAutoExpiry(learnings []Learning, now time.Time, report *MaintainReport) {
	for i := range learnings {
		l := &learnings[i]
		if l.Expired || l.SupersededBy != "" {
			continue
		}
		refTime := l.CreatedAt
		if l.LastAttachedAt != nil {
			refTime = *l.LastAttachedAt
		}
		if now.Sub(refTime) > autoExpiryThreshold {
			l.Expired = true
			report.AutoExpired++
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
	if opts.MinEffectiveness > 0 && l.Effectiveness() < opts.MinEffectiveness {
		return false
	}
	if opts.Category != "" && l.Category != opts.Category {
		return false
	}
	hasTags := len(opts.Tags) > 0
	hasPaths := len(opts.Paths) > 0
	hasSearch := opts.SearchText != ""
	if hasTags || hasPaths || hasSearch {
		tagMatch := hasTags && (matchesAnyTag(l.Tags, opts.Tags) || matchesAnyKeyword(l.Content, opts.Tags))
		pathMatch := hasPaths && matchesAnyPath(l.SourcePaths, opts.Paths)
		searchMatch := hasSearch && matchesSearchText(l, opts.SearchText)
		if !tagMatch && !pathMatch && !searchMatch {
			return false
		}
	}
	return true
}

// matchesSearchText returns true if the search text appears in Content, Summary, or Tags.
func matchesSearchText(l *Learning, text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(strings.ToLower(l.Content), lower) {
		return true
	}
	if strings.Contains(strings.ToLower(l.Summary), lower) {
		return true
	}
	for _, tag := range l.Tags {
		if strings.Contains(strings.ToLower(tag), lower) {
			return true
		}
	}
	return false
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
	learnings, skipped, err := s.readAllWithCount()
	if err != nil {
		return nil, err
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "warning: skipped %d corrupt lines in learning store\n", skipped)
	}
	return learnings, nil
}

func (s *Store) writeAll(learnings []Learning) error {
	data, err := marshalJSONL(learnings)
	if err != nil {
		return err
	}

	// Write unredacted to LocalDir (if set).
	if s.LocalDir != "" {
		if mkErr := os.MkdirAll(s.LocalDir, 0o755); mkErr == nil {
			localPath := filepath.Join(s.LocalDir, indexFile)
			if wErr := atomicWrite(localPath, data); wErr != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to write local learning store: %v\n", wErr)
			}
		}
	}

	// Write to SharedDir — redacted if scanner is set.
	if s.Scanner != nil {
		redacted := make([]Learning, len(learnings))
		copy(redacted, learnings)
		for i := range redacted {
			redacted[i].Content, _ = s.Scanner.Scan(redacted[i].Content)
			redacted[i].Summary, _ = s.Scanner.Scan(redacted[i].Summary)
		}
		data, err = marshalJSONL(redacted)
		if err != nil {
			return err
		}
	}

	sharedPath := filepath.Join(s.SharedDir, indexFile)
	return atomicWrite(sharedPath, data)
}

func marshalJSONL(learnings []Learning) ([]byte, error) {
	var buf strings.Builder
	for i := range learnings {
		data, err := json.Marshal(&learnings[i])
		if err != nil {
			return nil, fmt.Errorf("marshal learning %s: %w", learnings[i].ID, err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return []byte(buf.String()), nil
}

func (s *Store) withLock(fn func() error) error {
	lockPath := filepath.Join(s.SharedDir, ".lock")
	if err := os.MkdirAll(s.SharedDir, 0o755); err != nil {
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
	f, err := os.CreateTemp(dir, ".fabrikk-learning-*")
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
