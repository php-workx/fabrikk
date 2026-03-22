package learning

import (
	"testing"
)

func TestExtractKeywords(t *testing.T) {
	content := "Use withLock for atomic read-check-write operations. The withLock pattern prevents race conditions in concurrent file access."
	keywords := extractKeywords(content, 5)

	if len(keywords) == 0 {
		t.Fatal("expected keywords, got none")
	}
	if len(keywords) > 5 {
		t.Errorf("expected at most 5 keywords, got %d", len(keywords))
	}

	// "withlock" should be top keyword (appears twice).
	if keywords[0] != "withlock" {
		t.Errorf("top keyword = %q, want %q", keywords[0], "withlock")
	}
}

func TestExtractKeywordsExcludesStopWords(t *testing.T) {
	content := "the quick brown fox is a very fast animal"
	keywords := extractKeywords(content, 10)

	for _, kw := range keywords {
		if kw == "the" || kw == "is" || kw == "a" || kw == "very" {
			t.Errorf("stop word %q should be excluded", kw)
		}
	}
}

func TestExtractKeywordsShortWords(t *testing.T) {
	content := "go is ok to do an io op"
	keywords := extractKeywords(content, 10)

	// All words are < 3 chars or stop words — should return empty.
	if len(keywords) != 0 {
		t.Errorf("expected no keywords from short words, got %v", keywords)
	}
}

func TestKeywordsIndexedInTagIndex(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Add(&Learning{
		Tags:     []string{"compiler"},
		Category: CategoryPattern,
		Content:  "Requirement grouping by proximity reduces task count significantly",
		Summary:  "Proximity grouping",
	})

	// Query by a keyword from the content (not an explicit tag).
	results, err := store.Query(QueryOpts{Tags: []string{"grouping"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for content keyword 'grouping', got %d", len(results))
	}
}
