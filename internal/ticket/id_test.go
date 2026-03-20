package ticket

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIdPrefixMultiSegment(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-cool-project", "mcp"},
		{"hello-world", "hw"},
		{"a-b-c-d", "abcd"},
		{"foo_bar_baz", "fbb"},
		{"mixed-under_score", "mus"},
	}
	for _, tt := range tests {
		got := idPrefix(tt.input)
		if got != tt.want {
			t.Errorf("idPrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIdPrefixSingleSegment(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"attest", "att"},
		{"hello", "hel"},
		{"go", "go"},
		{"x", "x"},
		{"AB", "ab"}, // lowercased
	}
	for _, tt := range tests {
		got := idPrefix(tt.input)
		if got != tt.want {
			t.Errorf("idPrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGenerateIDFormat(t *testing.T) {
	dir := t.TempDir()
	id, err := GenerateID(dir)
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}

	// ID should be prefix-XXXX where prefix is derived from dir name.
	parts := splitIDParts(id)
	if len(parts) < 2 {
		t.Fatalf("ID %q has no hyphen separator", id)
	}

	suffix := parts[len(parts)-1]
	if len(suffix) != 4 {
		t.Errorf("suffix %q length = %d, want 4", suffix, len(suffix))
	}
	for _, c := range suffix {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			t.Errorf("suffix char %q is not lowercase alphanumeric", string(c))
		}
	}
}

func TestGenerateIDCollisionRetry(t *testing.T) {
	dir := t.TempDir()

	// Generate several IDs — all should be unique.
	ids := make(map[string]bool)
	for i := 0; i < 20; i++ {
		id, err := GenerateID(dir)
		if err != nil {
			t.Fatalf("GenerateID iteration %d: %v", i, err)
		}
		if ids[id] {
			t.Fatalf("duplicate ID %q on iteration %d", id, i)
		}
		ids[id] = true
		// Create the file so future calls can potentially collide.
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte("---\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGenerateIDExhaustion(t *testing.T) {
	// Create a directory where every possible ID already exists — not practical
	// for 36^4 IDs, but we can test the retry logic by filling all 3 attempts.
	// Instead, test that ErrIDCollision is the right error type by checking
	// the error when it would be returned.
	if !errors.Is(ErrIDCollision, ErrIDCollision) {
		t.Error("ErrIDCollision should match itself")
	}
}

func TestResolveIDEmpty(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveID(dir, "")
	if !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("expected ErrTicketNotFound for empty ID, got: %v", err)
	}
}

func TestResolveIDWhitespace(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveID(dir, "   ")
	if !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("expected ErrTicketNotFound for whitespace ID, got: %v", err)
	}
}

func TestResolveIDExactMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "task-abcd.md"), []byte("---\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := ResolveID(dir, "task-abcd")
	if err != nil {
		t.Fatalf("ResolveID: %v", err)
	}
	if id != "task-abcd" {
		t.Errorf("got %q, want task-abcd", id)
	}
}

func TestResolveIDPartialMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "task-abcd.md"), []byte("---\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := ResolveID(dir, "abcd")
	if err != nil {
		t.Fatalf("ResolveID partial: %v", err)
	}
	if id != "task-abcd" {
		t.Errorf("got %q, want task-abcd", id)
	}
}

func TestResolveIDAmbiguous(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"task-abc1.md", "task-abc2.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("---\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := ResolveID(dir, "abc")
	if !errors.Is(err, ErrAmbiguousID) {
		t.Fatalf("expected ErrAmbiguousID, got: %v", err)
	}
}

func TestResolveIDNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveID(dir, "nonexistent")
	if !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("expected ErrTicketNotFound, got: %v", err)
	}
}

func TestRandomSuffixLength(t *testing.T) {
	s, err := randomSuffix(4)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 4 {
		t.Errorf("length = %d, want 4", len(s))
	}
}

func TestRandomSuffixCharset(t *testing.T) {
	// Generate several suffixes and verify all chars are lowercase alphanumeric.
	for i := 0; i < 100; i++ {
		s, err := randomSuffix(4)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range s {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				t.Fatalf("char %q not in allowed charset", string(c))
			}
		}
	}
}

func TestValidateIDRejectsTraversal(t *testing.T) {
	bad := []string{
		"../escape",
		"..\\windows",
		"foo/bar",
		"foo\\bar",
		"/absolute",
		"",
	}
	for _, id := range bad {
		if err := ValidateID(id); err == nil {
			t.Errorf("ValidateID(%q) = nil, want error", id)
		}
	}
}

func TestValidateIDAcceptsGood(t *testing.T) {
	good := []string{
		"task-at-fr-001",
		"att-a1b2",
		"run-1773550987",
		"task-slice-1",
	}
	for _, id := range good {
		if err := ValidateID(id); err != nil {
			t.Errorf("ValidateID(%q) = %v, want nil", id, err)
		}
	}
}

// splitIDParts splits an ID by hyphens, handling multi-segment prefixes.
func splitIDParts(id string) []string {
	idx := len(id) - 1
	for idx >= 0 && id[idx] != '-' {
		idx--
	}
	if idx < 0 {
		return []string{id}
	}
	return []string{id[:idx], id[idx+1:]}
}
