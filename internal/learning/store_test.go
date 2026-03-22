package learning

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runger/attest/internal/state"
)

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestAddAndGet(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	l := &Learning{
		Tags:     []string{"compiler", "grouping"},
		Category: CategoryPattern,
		Content:  "Group requirements by source line proximity",
		Summary:  "Proximity grouping reduces task count",
	}
	if err := store.Add(l); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if l.ID == "" {
		t.Error("ID not generated")
	}

	got, err := store.Get(l.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Summary != l.Summary {
		t.Errorf("Summary = %q, want %q", got.Summary, l.Summary)
	}
	if got.Confidence != 0.5 {
		t.Errorf("Confidence = %f, want 0.5", got.Confidence)
	}
}

func TestQueryByTag(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{Tags: []string{"compiler"}, Category: CategoryPattern, Summary: "A"})
	_ = store.Add(&Learning{Tags: []string{"ticket"}, Category: CategoryAntiPattern, Summary: "B"})
	_ = store.Add(&Learning{Tags: []string{"compiler", "flock"}, Category: CategoryTooling, Summary: "C"})

	results, err := store.Query(QueryOpts{Tags: []string{"compiler"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func TestQueryByCategory(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "pat"})
	_ = store.Add(&Learning{Tags: []string{"b"}, Category: CategoryAntiPattern, Summary: "anti"})

	results, err := store.Query(QueryOpts{Category: CategoryAntiPattern})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Summary != "anti" {
		t.Fatalf("got %v, want [anti]", results)
	}
}

func TestQueryByPaths(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryCodebase, Summary: "engine", SourcePaths: []string{"internal/engine"}})
	_ = store.Add(&Learning{Tags: []string{"b"}, Category: CategoryCodebase, Summary: "ticket", SourcePaths: []string{"internal/ticket"}})

	results, err := store.Query(QueryOpts{Paths: []string{"internal/engine"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Summary != "engine" {
		t.Fatalf("got %v, want [engine]", results)
	}
}

func TestQuerySortByEffectiveness(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Low effectiveness: 1/5 tasks passed.
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "low", Confidence: 0.5, AttachCount: 5, SuccessCount: 1})
	// High effectiveness: 9/10 tasks passed.
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "high", Confidence: 0.5, AttachCount: 10, SuccessCount: 9})
	// Mid effectiveness: 3/5 tasks passed.
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "mid", Confidence: 0.5, AttachCount: 5, SuccessCount: 3})

	results, err := store.Query(QueryOpts{Tags: []string{"a"}, SortBy: "effectiveness"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d, want 3", len(results))
	}
	if results[0].Summary != "high" || results[1].Summary != "mid" || results[2].Summary != "low" {
		t.Errorf("wrong order: %s, %s, %s", results[0].Summary, results[1].Summary, results[2].Summary)
	}
}

func TestQueryLimit(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	for i := 0; i < 10; i++ {
		_ = store.Add(&Learning{Tags: []string{"bulk"}, Category: CategoryPattern, Summary: "item"})
	}

	results, err := store.Query(QueryOpts{Tags: []string{"bulk"}, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d, want 3", len(results))
	}
}

func TestQueryMinEffectiveness(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Low effectiveness (0.2 confidence, never attached).
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "low", Confidence: 0.2})
	// High effectiveness (0.8 confidence, never attached).
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "high", Confidence: 0.8})

	results, err := store.Query(QueryOpts{Tags: []string{"a"}, MinEffectiveness: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Summary != "high" {
		t.Fatalf("got %v, want [high]", results)
	}
}

func TestExpiredAndSupersededExcluded(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "active"})
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "expired", Expired: true})
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "superseded", SupersededBy: "lrn-other"})

	results, err := store.Query(QueryOpts{Tags: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Summary != "active" {
		t.Fatalf("got %v, want [active]", results)
	}
}

func TestHandoffWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	h := &SessionHandoff{
		RunID:     "run-1234",
		TaskID:    "task-a",
		Summary:   "Implemented grouping, tests passing",
		NextSteps: []string{"Wire into engine", "Add CLI"},
	}
	if err := store.WriteHandoff(h); err != nil {
		t.Fatalf("WriteHandoff: %v", err)
	}

	got, err := store.LatestHandoff()
	if err != nil {
		t.Fatalf("LatestHandoff: %v", err)
	}
	if got == nil {
		t.Fatal("no handoff returned")
	}
	if got.Summary != h.Summary {
		t.Errorf("Summary = %q, want %q", got.Summary, h.Summary)
	}
	if len(got.NextSteps) != 2 {
		t.Errorf("NextSteps len = %d, want 2", len(got.NextSteps))
	}
}

func TestMultipleHandoffWritesUpdatesLatest(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.WriteHandoff(&SessionHandoff{Summary: "first"})
	_ = store.WriteHandoff(&SessionHandoff{Summary: "second"})

	got, err := store.LatestHandoff()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Summary != "second" {
		t.Errorf("LatestHandoff = %v, want summary 'second'", got)
	}
}

func TestLatestHandoffNone(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	got, err := store.LatestHandoff()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil handoff when none exists")
	}
}

func TestGarbageCollect(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	store := &Store{Dir: dir, Now: fixedClock(now)}

	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "keep"})

	expired := &Learning{
		Tags:      []string{"a"},
		Category:  CategoryPattern,
		Summary:   "remove",
		Expired:   true,
		CreatedAt: now.Add(-100 * 24 * time.Hour),
	}
	_ = store.Add(expired)

	removed, err := store.GarbageCollect(90 * 24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	results, _ := store.Query(QueryOpts{Tags: []string{"a"}})
	if len(results) != 1 || results[0].Summary != "keep" {
		t.Errorf("after GC: %v, want [keep]", results)
	}
}

func TestTagIndexRebuilt(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{Tags: []string{"alpha", "beta"}, Category: CategoryPattern, Summary: "test"})

	data, err := os.ReadFile(filepath.Join(dir, "tags.json"))
	if err != nil {
		t.Fatalf("read tags.json: %v", err)
	}
	content := string(data)
	if !contains(content, "alpha") || !contains(content, "beta") {
		t.Errorf("tags.json missing expected tags: %s", content)
	}
}

func TestAddRejectsUnknownCategory(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	err := store.Add(&Learning{Tags: []string{"a"}, Category: "bogus", Summary: "test"})
	if err == nil {
		t.Error("expected error for unknown category")
	}
}

func TestAddDefaultsEmptyCategory(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	l := &Learning{Tags: []string{"a"}, Summary: "test"}
	if err := store.Add(l); err != nil {
		t.Fatal(err)
	}
	if l.Category != CategoryCodebase {
		t.Errorf("Category = %q, want %q", l.Category, CategoryCodebase)
	}
}

func TestGetNotFound(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, err := store.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

func TestAddDefaultsConfidence(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	l := &Learning{Tags: []string{"test"}, Category: CategoryPattern, Summary: "test"}
	_ = store.Add(l)

	got, _ := store.Get(l.ID)
	if got.Confidence != 0.5 {
		t.Errorf("Confidence = %f, want 0.5 (default)", got.Confidence)
	}
	if got.Source != "manual" {
		t.Errorf("Source = %q, want %q", got.Source, "manual")
	}
}

func TestTagNormalization(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{Tags: []string{"  UPPER  ", "Mixed"}, Category: CategoryPattern, Summary: "test"})

	results, err := store.Query(QueryOpts{Tags: []string{"upper"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("got %d, want 1 (case-insensitive tag match)", len(results))
	}
}

func TestReadAllSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{
		Tags:     []string{"test"},
		Category: CategoryPattern,
		Summary:  "valid entry",
	})

	indexPath := filepath.Join(dir, "index.jsonl")
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open index.jsonl: %v", err)
	}
	if _, err := f.WriteString("this is not valid json\n"); err != nil {
		_ = f.Close()
		t.Fatalf("write corrupt line: %v", err)
	}
	_ = f.Close()

	results, err := store.Query(QueryOpts{Tags: []string{"test"}})
	if err != nil {
		t.Fatalf("Query after corrupt line: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (valid entry survives corrupt line)", len(results))
	}
	if results[0].Summary != "valid entry" {
		t.Errorf("Summary = %q, want %q", results[0].Summary, "valid entry")
	}
}

func TestQueryLearnings(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{
		Tags:        []string{"compiler"},
		Category:    CategoryPattern,
		Content:     "Group requirements by source line proximity",
		Summary:     "Proximity grouping",
		SourcePaths: []string{"internal/compiler"},
		Confidence:  0.8,
	})
	_ = store.Add(&Learning{
		Tags:        []string{"ticket"},
		Category:    CategoryCodebase,
		Content:     "Ticket store uses YAML frontmatter",
		Summary:     "YAML frontmatter format",
		SourcePaths: []string{"internal/ticket"},
		Confidence:  0.6,
	})

	refs, err := store.QueryLearnings(state.LearningQueryOpts{
		Tags:  []string{"compiler"},
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("QueryLearnings: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d results, want 1", len(refs))
	}
	if refs[0].ID == "" {
		t.Error("ID should not be empty")
	}
	if refs[0].Category != string(CategoryPattern) {
		t.Errorf("Category = %q, want %q", refs[0].Category, CategoryPattern)
	}
	if refs[0].Summary != "Proximity grouping" {
		t.Errorf("Summary = %q, want %q", refs[0].Summary, "Proximity grouping")
	}
}

func TestAssembleContext(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	store := &Store{Dir: dir, Now: fixedClock(now)}

	for i := 0; i < 5; i++ {
		_ = store.Add(&Learning{
			Tags:       []string{"ctx-test"},
			Category:   CategoryPattern,
			Content:    strings.Repeat("x", 100), // ~25 tokens
			Summary:    "Test learning for context assembly",
			Confidence: 0.7,
		})
	}

	bundle, err := store.AssembleContext("task-1", []string{"ctx-test"}, nil, "")
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	if bundle.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", bundle.TaskID, "task-1")
	}
	if bundle.TokenBudget != 2000 {
		t.Errorf("TokenBudget = %d, want 2000", bundle.TokenBudget)
	}
	if bundle.TokensUsed <= 0 {
		t.Errorf("TokensUsed = %d, want > 0", bundle.TokensUsed)
	}
	if len(bundle.Learnings) == 0 {
		t.Error("Learnings should not be empty")
	}
}

func TestAssembleContextTokenBudget(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	for i := 0; i < 20; i++ {
		_ = store.Add(&Learning{
			Tags:       []string{"budget-test"},
			Category:   CategoryPattern,
			Content:    strings.Repeat("a", 500),
			Summary:    "Budget test learning",
			Confidence: 0.7,
		})
	}

	bundle, err := store.AssembleContext("task-budget", []string{"budget-test"}, nil, "")
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	if len(bundle.Learnings) >= 20 {
		t.Errorf("Learnings = %d, want < 20 (budget should cut off)", len(bundle.Learnings))
	}
	if bundle.TokensUsed > 2000 {
		t.Errorf("TokensUsed = %d, want <= 2000", bundle.TokensUsed)
	}
}

func TestMaintain_CorruptionBlocks(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "valid"})

	indexPath := filepath.Join(dir, "index.jsonl")
	f, _ := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("this is not valid json\n")
	_ = f.Close()

	_, err := store.Maintain(90 * 24 * time.Hour)
	if err == nil {
		t.Fatal("expected error for corrupt store")
	}
	if !errors.Is(err, ErrCorruptLearningStore) {
		t.Errorf("error = %v, want ErrCorruptLearningStore", err)
	}
}

func TestMaintain_ConcurrentSkips(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "test"})

	store.maintaining.Store(true)
	report, err := store.Maintain(90 * 24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Skipped {
		t.Error("expected Skipped=true when maintenance already running")
	}
	store.maintaining.Store(false)
}

func TestAddBlockedByCorruption(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "valid"})

	indexPath := filepath.Join(dir, "index.jsonl")
	f, _ := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("not valid json\n")
	_ = f.Close()

	err := store.Add(&Learning{Tags: []string{"b"}, Category: CategoryPattern, Summary: "new"})
	if err == nil {
		t.Fatal("expected error for corrupt store")
	}
	if !errors.Is(err, ErrCorruptLearningStore) {
		t.Errorf("error = %v, want ErrCorruptLearningStore", err)
	}

	data, _ := os.ReadFile(indexPath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines preserved, got %d", len(lines))
	}
}

// --- Regression tests ---

func TestAssembleContextTagPathUnion(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{
		Tags: []string{"compiler"}, Category: CategoryPattern,
		Content: "tag-only learning", Summary: "tag-only", Confidence: 0.8,
	})
	_ = store.Add(&Learning{
		Tags: []string{"unrelated"}, Category: CategoryCodebase,
		Content: "path-only learning", Summary: "path-only", Confidence: 0.8,
		SourcePaths: []string{"internal/engine"},
	})

	bundle, err := store.AssembleContext("task-1", []string{"compiler"}, []string{"internal/engine"}, "")
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	if len(bundle.Learnings) != 2 {
		names := make([]string, len(bundle.Learnings))
		for i := range bundle.Learnings {
			names[i] = bundle.Learnings[i].Summary
		}
		t.Fatalf("got %d learnings %v, want 2 (union of tag + path matches)", len(bundle.Learnings), names)
	}
}

func TestAssembleContextRejectsOversizedFirstLearning(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{
		Tags: []string{"big"}, Category: CategoryPattern,
		Content: strings.Repeat("x", 10240), Summary: "oversized", Confidence: 0.9,
	})

	bundle, err := store.AssembleContext("task-1", []string{"big"}, nil, "")
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	if bundle.TokensUsed > 2000 {
		t.Errorf("TokensUsed = %d, want <= 2000", bundle.TokensUsed)
	}
	if len(bundle.Learnings) != 0 {
		t.Errorf("Learnings = %d, want 0 (oversized learning exceeds budget)", len(bundle.Learnings))
	}
}

func TestAssembleContextMinEffectivenessThreshold(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{
		Tags: []string{"low-e"}, Category: CategoryPattern,
		Content: "low effectiveness learning", Summary: "low eff", Confidence: 0.2,
	})
	_ = store.Add(&Learning{
		Tags: []string{"low-e"}, Category: CategoryPattern,
		Content: "high effectiveness learning", Summary: "high eff", Confidence: 0.8,
	})

	bundle, err := store.AssembleContext("task-1", []string{"low-e"}, nil, "")
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	if len(bundle.Learnings) != 1 {
		t.Fatalf("got %d learnings, want 1 (effectiveness 0.2 should be excluded)", len(bundle.Learnings))
	}
	if bundle.Learnings[0].Summary != "high eff" {
		t.Errorf("included learning = %q, want 'high eff'", bundle.Learnings[0].Summary)
	}
}

func TestPathMatchingAbsoluteRelative(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{
		Tags: []string{"a"}, Category: CategoryCodebase,
		Content: "engine insight", Summary: "engine", Confidence: 0.8,
		SourcePaths: []string{"internal/engine"},
	})

	results, err := store.Query(QueryOpts{
		Paths: []string{"/Users/someone/project/internal/engine"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1 (absolute path should match relative stored path)", len(results))
	}
}

// --- New outcome-based tests ---

func TestRecordOutcome(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	l := &Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "test"}
	_ = store.Add(l)

	if err := store.RecordOutcome([]string{l.ID}, true); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}

	got, _ := store.Get(l.ID)
	if got.AttachCount != 1 {
		t.Errorf("AttachCount = %d, want 1", got.AttachCount)
	}
	if got.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", got.SuccessCount)
	}
	if got.LastAttachedAt == nil {
		t.Error("LastAttachedAt should be set")
	}
}

func TestRecordOutcomePassFail(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	l := &Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "test"}
	_ = store.Add(l)

	// Record a pass.
	_ = store.RecordOutcome([]string{l.ID}, true)
	// Record a fail.
	_ = store.RecordOutcome([]string{l.ID}, false)

	got, _ := store.Get(l.ID)
	if got.AttachCount != 2 {
		t.Errorf("AttachCount = %d, want 2", got.AttachCount)
	}
	if got.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1 (only pass increments)", got.SuccessCount)
	}
}

func TestEffectivenessScoring(t *testing.T) {
	// No attachments → falls back to Confidence.
	l1 := &Learning{Confidence: 0.8}
	if eff := l1.Effectiveness(); eff != 0.8 {
		t.Errorf("Effectiveness (no attachments) = %f, want 0.8 (confidence)", eff)
	}

	// With attachments → SuccessCount/AttachCount.
	l2 := &Learning{Confidence: 0.8, AttachCount: 10, SuccessCount: 7}
	if eff := l2.Effectiveness(); eff != 0.7 {
		t.Errorf("Effectiveness (7/10) = %f, want 0.7", eff)
	}

	// All failures → 0.0.
	l3 := &Learning{Confidence: 0.8, AttachCount: 5, SuccessCount: 0}
	if eff := l3.Effectiveness(); eff != 0.0 {
		t.Errorf("Effectiveness (0/5) = %f, want 0.0", eff)
	}
}

func TestMaintain_AutoExpiry(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	store := &Store{Dir: dir, Now: fixedClock(now)}

	// Learning created 100 days ago, never attached.
	_ = store.Add(&Learning{
		Tags:      []string{"old"},
		Category:  CategoryPattern,
		Summary:   "should expire",
		CreatedAt: now.Add(-100 * 24 * time.Hour),
	})

	// Learning created 100 days ago, but attached 10 days ago.
	attached := now.Add(-10 * 24 * time.Hour)
	_ = store.Add(&Learning{
		Tags:           []string{"old"},
		Category:       CategoryPattern,
		Summary:        "should survive",
		CreatedAt:      now.Add(-100 * 24 * time.Hour),
		LastAttachedAt: &attached,
	})

	report, err := store.Maintain(365 * 24 * time.Hour) // large maxAge so GC doesn't remove
	if err != nil {
		t.Fatal(err)
	}
	if report.AutoExpired != 1 {
		t.Errorf("AutoExpired = %d, want 1", report.AutoExpired)
	}

	results, _ := store.Query(QueryOpts{Tags: []string{"old"}})
	if len(results) != 1 || results[0].Summary != "should survive" {
		t.Errorf("after auto-expiry: got %v, want [should survive]", results)
	}
}

func TestRecordOutcomeBatch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	l1 := &Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "first"}
	l2 := &Learning{Tags: []string{"b"}, Category: CategoryPattern, Summary: "second"}
	_ = store.Add(l1)
	_ = store.Add(l2)

	if err := store.RecordOutcome([]string{l1.ID, l2.ID}, true); err != nil {
		t.Fatalf("RecordOutcome batch: %v", err)
	}

	got1, _ := store.Get(l1.ID)
	got2, _ := store.Get(l2.ID)
	if got1.AttachCount != 1 || got1.SuccessCount != 1 {
		t.Errorf("l1: AttachCount=%d SuccessCount=%d, want 1/1", got1.AttachCount, got1.SuccessCount)
	}
	if got2.AttachCount != 1 || got2.SuccessCount != 1 {
		t.Errorf("l2: AttachCount=%d SuccessCount=%d, want 1/1", got2.AttachCount, got2.SuccessCount)
	}
}

func TestSearchText(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{Tags: []string{"compiler"}, Category: CategoryPattern, Content: "Use flock for concurrent file access", Summary: "flock concurrency"})
	_ = store.Add(&Learning{Tags: []string{"ticket"}, Category: CategoryCodebase, Content: "YAML frontmatter format for tickets", Summary: "ticket format"})
	_ = store.Add(&Learning{Tags: []string{"engine"}, Category: CategoryTooling, Content: "Run gofumpt before committing", Summary: "formatting"})

	// Search by content keyword.
	results, err := store.Query(QueryOpts{SearchText: "flock"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Summary != "flock concurrency" {
		t.Fatalf("search 'flock': got %v, want [flock concurrency]", results)
	}

	// Search by summary keyword.
	results, err = store.Query(QueryOpts{SearchText: "formatting"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Summary != "formatting" {
		t.Fatalf("search 'formatting': got %v, want [formatting]", results)
	}

	// Case-insensitive.
	results, err = store.Query(QueryOpts{SearchText: "YAML"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("search 'YAML': got %d, want 1", len(results))
	}

	// No match.
	results, err = store.Query(QueryOpts{SearchText: "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("search 'nonexistent': got %d, want 0", len(results))
	}

	// Search combined with tag filter.
	results, err = store.Query(QueryOpts{Tags: []string{"compiler"}, SearchText: "concurrent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("search 'concurrent' + tag 'compiler': got %d, want 1", len(results))
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
