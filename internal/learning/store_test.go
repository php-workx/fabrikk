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
	defer store.Wait()

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
	defer store.Wait()

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
	defer store.Wait()

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
	defer store.Wait()

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

func TestQuerySortByUtility(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "low", Utility: 0.3})
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "high", Utility: 0.9})
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "mid", Utility: 0.6})

	results, err := store.Query(QueryOpts{Tags: []string{"a"}, SortBy: "utility"})
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
	defer store.Wait()

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

func TestQueryMinUtility(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "low", Utility: 0.2})
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "high", Utility: 0.8})

	results, err := store.Query(QueryOpts{Tags: []string{"a"}, MinUtility: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Summary != "high" {
		t.Fatalf("got %v, want [high]", results)
	}
}

func TestRecordCitation(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	l := &Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "test"}
	_ = store.Add(l)

	if err := store.RecordCitation(l.ID); err != nil {
		t.Fatalf("RecordCitation: %v", err)
	}

	got, _ := store.Get(l.ID)
	if got.CitedCount != 1 {
		t.Errorf("CitedCount = %d, want 1", got.CitedCount)
	}
	if got.LastCitedAt == nil {
		t.Error("LastCitedAt should be set")
	}
}

func TestUtilityDecay(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	store := &Store{Dir: dir, Now: fixedClock(now)}
	defer store.Wait()

	// Add a learning created 60 days ago, never cited.
	old := &Learning{
		Tags:      []string{"stale"},
		Category:  CategoryPattern,
		Summary:   "old learning",
		Utility:   0.5,
		CreatedAt: now.Add(-60 * 24 * time.Hour),
	}
	_ = store.Add(old)

	results, err := store.Query(QueryOpts{Tags: []string{"stale"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d, want 1", len(results))
	}
	// Utility should have decayed by 0.05.
	if results[0].Utility != 0.45 {
		t.Errorf("Utility = %f, want 0.45 (decayed)", results[0].Utility)
	}
}

func TestUtilityDecayDoesNotAccumulate(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	store := &Store{Dir: dir, Now: fixedClock(now)}
	defer store.Wait()

	_ = store.Add(&Learning{
		Tags:      []string{"stale"},
		Category:  CategoryPattern,
		Summary:   "old",
		Utility:   0.5,
		CreatedAt: now.Add(-60 * 24 * time.Hour),
	})

	// Query twice — decay is lazy and not persisted, so both should return 0.45.
	r1, _ := store.Query(QueryOpts{Tags: []string{"stale"}})
	r2, _ := store.Query(QueryOpts{Tags: []string{"stale"}})

	if r1[0].Utility != 0.45 {
		t.Errorf("first query: Utility = %f, want 0.45", r1[0].Utility)
	}
	if r2[0].Utility != 0.45 {
		t.Errorf("second query: Utility = %f, want 0.45 (should not double-decay)", r2[0].Utility)
	}
}

func TestExpiredAndSupersededExcluded(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

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
	defer store.Wait()

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
	defer store.Wait()

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
	defer store.Wait()

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
	defer store.Wait()

	// Active learning.
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "keep"})

	// Expired learning created 100 days ago.
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

	// Verify only active learning remains.
	results, _ := store.Query(QueryOpts{Tags: []string{"a"}})
	if len(results) != 1 || results[0].Summary != "keep" {
		t.Errorf("after GC: %v, want [keep]", results)
	}
}

func TestTagIndexRebuilt(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	_ = store.Add(&Learning{Tags: []string{"alpha", "beta"}, Category: CategoryPattern, Summary: "test"})

	// Verify tags.json exists and has content.
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
	defer store.Wait()

	err := store.Add(&Learning{Tags: []string{"a"}, Category: "bogus", Summary: "test"})
	if err == nil {
		t.Error("expected error for unknown category")
	}
}

func TestAddDefaultsEmptyCategory(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

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
	defer store.Wait()

	_, err := store.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

func TestAddPreservesExplicitZeroConfidenceAndUtility(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	l := &Learning{
		Tags:       []string{"test"},
		Category:   CategoryPattern,
		Summary:    "explicit zeros",
		Confidence: 0.0,
		Utility:    0.0,
	}
	if err := store.Add(l); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := store.Get(l.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// New learnings with zero values should get defaults.
	if got.Confidence != 0.5 {
		t.Errorf("new learning Confidence = %f, want 0.5 (default)", got.Confidence)
	}
	if got.Utility != 0.5 {
		t.Errorf("new learning Utility = %f, want 0.5 (default)", got.Utility)
	}

	// Now test with a pre-set ID (simulating re-add or external creation).
	l2 := &Learning{
		ID:         "lrn-explicit",
		Tags:       []string{"test"},
		Category:   CategoryPattern,
		Summary:    "pre-set ID with zeros",
		Confidence: 0.0,
		Utility:    0.0,
	}
	if err := store.Add(l2); err != nil {
		t.Fatalf("Add with ID: %v", err)
	}

	got2, err := store.Get("lrn-explicit")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Pre-set ID learnings should preserve explicit zero values.
	if got2.Confidence != 0.0 {
		t.Errorf("pre-set ID Confidence = %f, want 0.0 (preserved)", got2.Confidence)
	}
	if got2.Utility != 0.0 {
		t.Errorf("pre-set ID Utility = %f, want 0.0 (preserved)", got2.Utility)
	}
}

func TestTagNormalization(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	_ = store.Add(&Learning{Tags: []string{"  UPPER  ", "Mixed"}, Category: CategoryPattern, Summary: "test"})

	// Query with lowercase should match.
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
	defer store.Wait()

	// Add a valid learning first so we know the format.
	_ = store.Add(&Learning{
		Tags:     []string{"test"},
		Category: CategoryPattern,
		Summary:  "valid entry",
	})

	// Append a corrupt line to the JSONL file.
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

	// Query should still return the valid learning.
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
	defer store.Wait()

	_ = store.Add(&Learning{
		Tags:        []string{"compiler"},
		Category:    CategoryPattern,
		Content:     "Group requirements by source line proximity",
		Summary:     "Proximity grouping",
		SourcePaths: []string{"internal/compiler"},
		Utility:     0.8,
	})
	_ = store.Add(&Learning{
		Tags:        []string{"ticket"},
		Category:    CategoryCodebase,
		Content:     "Ticket store uses YAML frontmatter",
		Summary:     "YAML frontmatter format",
		SourcePaths: []string{"internal/ticket"},
		Utility:     0.6,
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
	if refs[0].Utility != 0.8 {
		t.Errorf("Utility = %f, want 0.8", refs[0].Utility)
	}
	if refs[0].Summary != "Proximity grouping" {
		t.Errorf("Summary = %q, want %q", refs[0].Summary, "Proximity grouping")
	}
}

func TestAssembleContext(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	store := &Store{Dir: dir, Now: fixedClock(now)}
	defer store.Wait()

	// Add several learnings with ~100 char content (~25 tokens each).
	for i := 0; i < 5; i++ {
		_ = store.Add(&Learning{
			Tags:     []string{"ctx-test"},
			Category: CategoryPattern,
			Content:  strings.Repeat("x", 100), // ~25 tokens
			Summary:  "Test learning for context assembly",
			Utility:  0.7,
		})
	}

	bundle, err := store.AssembleContext("task-1", []string{"ctx-test"}, nil)
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
	defer store.Wait()

	// Add 20 learnings each with 500-char content (~125 tokens each).
	// Total ~2500 tokens > 2000 budget.
	for i := 0; i < 20; i++ {
		_ = store.Add(&Learning{
			Tags:     []string{"budget-test"},
			Category: CategoryPattern,
			Content:  strings.Repeat("a", 500),
			Summary:  "Budget test learning",
			Utility:  0.7,
		})
	}

	bundle, err := store.AssembleContext("task-budget", []string{"budget-test"}, nil)
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

func TestSourceAndMaturityPreserved(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	l := &Learning{
		Tags:          []string{"test"},
		Category:      CategoryPattern,
		Summary:       "test",
		Source:        "council",
		SourceFinding: "sec-001",
		Maturity:      MaturityCandidate,
	}
	if err := store.Add(l); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "council" {
		t.Errorf("Source = %q, want %q", got.Source, "council")
	}
	if got.SourceFinding != "sec-001" {
		t.Errorf("SourceFinding = %q, want %q", got.SourceFinding, "sec-001")
	}
	if got.Maturity != MaturityCandidate {
		t.Errorf("Maturity = %q, want %q", got.Maturity, MaturityCandidate)
	}
}

func TestAddDefaultsSourceAndMaturity(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	l := &Learning{Tags: []string{"test"}, Category: CategoryPattern, Summary: "test"}
	_ = store.Add(l)

	got, _ := store.Get(l.ID)
	if got.Source != "manual" {
		t.Errorf("Source default = %q, want %q", got.Source, "manual")
	}
	if got.Maturity != MaturityProvisional {
		t.Errorf("Maturity default = %q, want %q", got.Maturity, MaturityProvisional)
	}
}

func TestMaturityWeightedQuery(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	// Two learnings with same utility but different maturity
	_ = store.Add(&Learning{
		Tags:     []string{"test"},
		Category: CategoryPattern,
		Summary:  "provisional",
		Utility:  0.5,
		Maturity: MaturityProvisional,
	})
	_ = store.Add(&Learning{
		Tags:     []string{"test"},
		Category: CategoryPattern,
		Summary:  "established",
		Utility:  0.5,
		Maturity: MaturityEstablished,
	})

	results, err := store.Query(QueryOpts{Tags: []string{"test"}, SortBy: "utility"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	// Established (0.5 * 1.5 = 0.75) should rank above provisional (0.5 * 1.0 = 0.5)
	if results[0].Summary != "established" {
		t.Errorf("first result = %q, want 'established' (higher maturity weight)", results[0].Summary)
	}
}

func TestMaintain_MaturityPromotion(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	store := &Store{Dir: dir, Now: fixedClock(now)}
	defer store.Wait()

	l := &Learning{
		Tags:       []string{"test"},
		Category:   CategoryPattern,
		Summary:    "promotable",
		Utility:    0.6,
		CitedCount: 3,
		Maturity:   MaturityProvisional,
	}
	_ = store.Add(l)

	report, err := store.Maintain(90 * 24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if report.Promoted != 1 {
		t.Errorf("Promoted = %d, want 1", report.Promoted)
	}

	got, _ := store.Get(l.ID)
	if got.Maturity != MaturityCandidate {
		t.Errorf("Maturity = %q, want candidate", got.Maturity)
	}
}

func TestMaintain_MaturityDemotion(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	store := &Store{Dir: dir, Now: fixedClock(now)}
	defer store.Wait()

	l := &Learning{
		Tags:     []string{"test"},
		Category: CategoryPattern,
		Summary:  "demotable",
		Utility:  0.2,
		Maturity: MaturityCandidate,
	}
	_ = store.Add(l)

	report, err := store.Maintain(90 * 24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if report.Demoted != 1 {
		t.Errorf("Demoted = %d, want 1", report.Demoted)
	}

	got, _ := store.Get(l.ID)
	if got.Maturity != MaturityProvisional {
		t.Errorf("Maturity = %q, want provisional", got.Maturity)
	}
}

func TestMaintain_StalePenalty(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	store := &Store{Dir: dir, Now: fixedClock(now)}
	defer store.Wait()

	l := &Learning{
		Tags:      []string{"test"},
		Category:  CategoryPattern,
		Summary:   "stale",
		Utility:   0.5,
		CreatedAt: now.Add(-60 * 24 * time.Hour), // 60 days old, never cited
	}
	_ = store.Add(l)

	report, err := store.Maintain(90 * 24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if report.Stale != 1 {
		t.Errorf("Stale = %d, want 1", report.Stale)
	}

	got, _ := store.Get(l.ID)
	if got.Utility != 0.4 {
		t.Errorf("Utility = %f, want 0.4 (penalized by 0.1)", got.Utility)
	}
}

func TestMaintain_CorruptionBlocks(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	// Add a valid learning
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "valid"})

	// Corrupt the store by appending invalid JSON
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
	defer store.Wait()
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "test"})

	// Simulate concurrent maintenance
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
	defer store.Wait()

	// Add a valid learning
	_ = store.Add(&Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "valid"})

	// Corrupt the store
	indexPath := filepath.Join(dir, "index.jsonl")
	f, _ := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("not valid json\n")
	_ = f.Close()

	// Add should fail
	err := store.Add(&Learning{Tags: []string{"b"}, Category: CategoryPattern, Summary: "new"})
	if err == nil {
		t.Fatal("expected error for corrupt store")
	}
	if !errors.Is(err, ErrCorruptLearningStore) {
		t.Errorf("error = %v, want ErrCorruptLearningStore", err)
	}

	// Verify original data preserved (corrupt line still there)
	data, _ := os.ReadFile(indexPath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 { // 1 valid + 1 corrupt
		t.Errorf("expected 2 lines preserved, got %d", len(lines))
	}
}

func TestRecordCitationBlockedByCorruption(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	l := &Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "test"}
	_ = store.Add(l)

	// Corrupt the store
	indexPath := filepath.Join(dir, "index.jsonl")
	f, _ := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("corrupt\n")
	_ = f.Close()

	err := store.RecordCitation(l.ID)
	if !errors.Is(err, ErrCorruptLearningStore) {
		t.Errorf("error = %v, want ErrCorruptLearningStore", err)
	}
}

// --- Regression tests for code review issues ---

// Issue 1: Tag+path lookup should be union, not intersection.
func TestAssembleContextTagPathUnion(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	// Learning with only a matching tag.
	_ = store.Add(&Learning{
		Tags:     []string{"compiler"},
		Category: CategoryPattern,
		Content:  "tag-only learning",
		Summary:  "tag-only",
		Utility:  0.8,
	})
	// Learning with only a matching path.
	_ = store.Add(&Learning{
		Tags:        []string{"unrelated"},
		Category:    CategoryCodebase,
		Content:     "path-only learning",
		Summary:     "path-only",
		Utility:     0.8,
		SourcePaths: []string{"internal/engine"},
	})

	bundle, err := store.AssembleContext("task-1", []string{"compiler"}, []string{"internal/engine"})
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

// Issue 2: AssembleContext should record citations.
func TestAssembleContextRecordsCitations(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	l := &Learning{
		Tags:     []string{"test"},
		Category: CategoryPattern,
		Content:  "citeable learning",
		Summary:  "citeable",
		Utility:  0.8,
	}
	_ = store.Add(l)

	_, err := store.AssembleContext("task-1", []string{"test"}, nil)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}

	got, _ := store.Get(l.ID)
	if got.CitedCount != 1 {
		t.Errorf("CitedCount = %d after AssembleContext, want 1", got.CitedCount)
	}
	if got.LastCitedAt == nil {
		t.Error("LastCitedAt should be set after AssembleContext")
	}
}

// Issue 3: Oversized first learning must not exceed token budget.
func TestAssembleContextRejectsOversizedFirstLearning(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	// 10KB content → ~2560 tokens, exceeds 2000 budget.
	_ = store.Add(&Learning{
		Tags:     []string{"big"},
		Category: CategoryPattern,
		Content:  strings.Repeat("x", 10240),
		Summary:  "oversized",
		Utility:  0.9,
	})

	bundle, err := store.AssembleContext("task-1", []string{"big"}, nil)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	if bundle.TokensUsed > 2000 {
		t.Errorf("TokensUsed = %d, want <= 2000 (oversized first learning should be skipped)", bundle.TokensUsed)
	}
	if len(bundle.Learnings) != 0 {
		t.Errorf("Learnings = %d, want 0 (oversized learning exceeds budget)", len(bundle.Learnings))
	}
}

// Issue 4: AssembleContext MinUtility should be 0.3, not 0.1.
func TestAssembleContextMinUtilityThreshold(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	_ = store.Add(&Learning{
		Tags:     []string{"low-u"},
		Category: CategoryPattern,
		Content:  "low utility learning",
		Summary:  "low utility",
		Utility:  0.2,
	})
	_ = store.Add(&Learning{
		Tags:     []string{"low-u"},
		Category: CategoryPattern,
		Content:  "high utility learning",
		Summary:  "high utility",
		Utility:  0.8,
	})

	bundle, err := store.AssembleContext("task-1", []string{"low-u"}, nil)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	if len(bundle.Learnings) != 1 {
		t.Fatalf("got %d learnings, want 1 (utility 0.2 should be excluded)", len(bundle.Learnings))
	}
	if bundle.Learnings[0].Summary != "high utility" {
		t.Errorf("included learning = %q, want 'high utility'", bundle.Learnings[0].Summary)
	}
}

// Issue 5: Path matching should handle absolute/relative mismatch.
func TestPathMatchingAbsoluteRelative(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	_ = store.Add(&Learning{
		Tags:        []string{"a"},
		Category:    CategoryCodebase,
		Content:     "engine insight",
		Summary:     "engine",
		Utility:     0.8,
		SourcePaths: []string{"internal/engine"},
	})

	// Query with absolute path that ends in the same relative path.
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

// Issue 6: MaintainIfStale should trigger on fresh store.
func TestMaintainIfStaleTriggersOnFreshStore(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	// Add a stale learning that should be penalized by maintenance.
	now := time.Now()
	_ = store.Add(&Learning{
		Tags:      []string{"stale"},
		Category:  CategoryPattern,
		Summary:   "stale learning",
		Utility:   0.5,
		CreatedAt: now.Add(-60 * 24 * time.Hour), // 60 days old, never cited
	})

	// MaintainIfStale should trigger on fresh store (lastMaintainedAt is zero).
	store.MaintainIfStale()
	store.Wait() // wait for background maintenance to complete

	// After maintenance, the stale learning should have been penalized.
	got, err := store.Query(QueryOpts{Tags: []string{"stale"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	// Utility should have been reduced by staleness penalty (0.1).
	if got[0].Utility >= 0.5 {
		t.Errorf("Utility = %f after maintenance on fresh store, want < 0.5 (maintenance should have triggered)", got[0].Utility)
	}
}

// Issue 2 (batch): RecordCitations batches multiple updates in one write.
func TestRecordCitationsBatch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	defer store.Wait()

	l1 := &Learning{Tags: []string{"a"}, Category: CategoryPattern, Summary: "first"}
	l2 := &Learning{Tags: []string{"b"}, Category: CategoryPattern, Summary: "second"}
	_ = store.Add(l1)
	_ = store.Add(l2)

	if err := store.RecordCitations([]string{l1.ID, l2.ID}); err != nil {
		t.Fatalf("RecordCitations: %v", err)
	}

	got1, _ := store.Get(l1.ID)
	got2, _ := store.Get(l2.ID)
	if got1.CitedCount != 1 {
		t.Errorf("l1 CitedCount = %d, want 1", got1.CitedCount)
	}
	if got2.CitedCount != 1 {
		t.Errorf("l2 CitedCount = %d, want 1", got2.CitedCount)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
