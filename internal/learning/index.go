package learning

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// rebuildIndex builds the tag inverted index from learnings and writes tags.json.
// Tags from each learning and keywords extracted from content are both indexed.
func (s *Store) rebuildIndex(learnings []Learning) error {
	idx := TagIndex{Tags: make(map[string][]string)}
	for i := range learnings {
		if learnings[i].Expired || learnings[i].SupersededBy != "" {
			continue
		}
		for _, tag := range learnings[i].Tags {
			idx.Tags[tag] = append(idx.Tags[tag], learnings[i].ID)
		}
		// Extract keywords from content (top 10 significant words).
		for _, kw := range extractKeywords(learnings[i].Content, 10) {
			idx.Tags[kw] = append(idx.Tags[kw], learnings[i].ID)
		}
	}
	idx.Version++

	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	return atomicWrite(filepath.Join(s.SharedDir, "tags.json"), data)
}

// stopWords are common words excluded from keyword extraction.
var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "by": true, "do": true, "for": true, "from": true, "has": true,
	"have": true, "he": true, "in": true, "is": true, "it": true, "its": true,
	"of": true, "on": true, "or": true, "she": true, "that": true, "the": true,
	"this": true, "to": true, "was": true, "we": true, "were": true, "will": true,
	"with": true, "not": true, "but": true, "can": true, "if": true, "no": true,
	"so": true, "than": true, "too": true, "very": true, "just": true,
	"about": true, "also": true, "been": true, "does": true, "had": true,
	"may": true, "more": true, "should": true, "some": true, "such": true,
	"then": true, "them": true, "they": true, "when": true, "which": true,
	"who": true, "would": true, "all": true, "each": true, "into": true,
	"most": true, "only": true, "other": true, "our": true, "out": true,
	"over": true, "own": true, "same": true, "their": true, "there": true,
	"these": true, "those": true, "through": true, "under": true, "up": true,
	"what": true, "where": true, "your": true, "you": true, "use": true,
	"using": true, "used": true,
}

// extractKeywords returns the top N significant words from content.
// Words are lowercased, stop words are excluded, and words shorter than
// 3 characters are skipped. Returned in frequency-descending order.
func extractKeywords(content string, maxKeywords int) []string {
	freq := make(map[string]int)
	for _, word := range tokenize(content) {
		word = strings.ToLower(word)
		if len(word) < 3 || stopWords[word] {
			continue
		}
		freq[word]++
	}

	// Sort by frequency descending, then alphabetically for stability.
	type kv struct {
		word  string
		count int
	}
	pairs := make([]kv, 0, len(freq))
	for w, c := range freq {
		pairs = append(pairs, kv{w, c})
	}
	// Simple insertion sort — keyword lists are small.
	for i := 1; i < len(pairs); i++ {
		for j := i; j > 0; j-- {
			if pairs[j].count > pairs[j-1].count ||
				(pairs[j].count == pairs[j-1].count && pairs[j].word < pairs[j-1].word) {
				pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
			}
		}
	}

	result := make([]string, 0, maxKeywords)
	for i := 0; i < len(pairs) && i < maxKeywords; i++ {
		result = append(result, pairs[i].word)
	}
	return result
}

// tokenize splits content into words, stripping punctuation.
func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	})
}

// matchesAnyKeyword returns true if any query tag matches a keyword
// extracted from the content.
func matchesAnyKeyword(content string, queryTags []string) bool {
	keywords := extractKeywords(content, 10)
	for _, qt := range queryTags {
		qt = strings.ToLower(strings.TrimSpace(qt))
		for _, kw := range keywords {
			if qt == kw {
				return true
			}
		}
	}
	return false
}
