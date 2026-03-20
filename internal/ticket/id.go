package ticket

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GenerateID creates a new ticket ID matching Ticket's {prefix}-{4char} format.
// The prefix is derived from the directory name. Checks for collisions.
func GenerateID(dir string) (string, error) {
	prefix := idPrefix(filepath.Base(dir))

	for attempt := 0; attempt < 3; attempt++ {
		suffix, err := randomSuffix(4)
		if err != nil {
			return "", fmt.Errorf("generate random suffix: %w", err)
		}
		id := prefix + "-" + suffix
		path := filepath.Join(dir, id+".md")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return id, nil
		}
	}
	return "", ErrIDCollision
}

// ValidateID checks that an ID is safe to use in file path construction.
// Rejects IDs containing path separators, ".." segments, or absolute paths.
func ValidateID(id string) error {
	if id == "" {
		return fmt.Errorf("empty ticket ID")
	}
	if filepath.IsAbs(id) {
		return fmt.Errorf("ticket ID must not be absolute: %q", id)
	}
	if strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("ticket ID must not contain path separators: %q", id)
	}
	if strings.Contains(id, "..") {
		return fmt.Errorf("ticket ID must not contain '..': %q", id)
	}
	return nil
}

// ResolveID resolves a (possibly partial) ticket ID to the full ID.
// Supports exact match and substring matching with ambiguity detection.
func ResolveID(dir, partial string) (string, error) {
	partial = strings.TrimSpace(partial)
	if partial == "" {
		return "", fmt.Errorf("%w: empty ID", ErrTicketNotFound)
	}
	if err := ValidateID(partial); err != nil {
		return "", fmt.Errorf("%w: %v", ErrTicketNotFound, err)
	}

	// Try exact match first.
	exactPath := filepath.Join(dir, partial+".md")
	if _, err := os.Stat(exactPath); err == nil {
		return partial, nil
	}

	// Glob for partial match.
	pattern := filepath.Join(dir, "*"+partial+"*.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("glob: %w", err)
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w: no ticket matching %q", ErrTicketNotFound, partial)
	case 1:
		name := filepath.Base(matches[0])
		return strings.TrimSuffix(name, ".md"), nil
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = strings.TrimSuffix(filepath.Base(m), ".md")
		}
		return "", fmt.Errorf("%w: %q matches %d tickets: %s",
			ErrAmbiguousID, partial, len(matches), strings.Join(ids, ", "))
	}
}

// idPrefix generates a prefix from a directory name.
// Takes first letter of each hyphen/underscore segment.
// e.g., "my-cool-project" → "mcp", "attest" → "att"
func idPrefix(dirName string) string {
	dirName = strings.ToLower(dirName)
	parts := strings.FieldsFunc(dirName, func(r rune) bool {
		return r == '-' || r == '_'
	})

	if len(parts) <= 1 {
		if len(dirName) >= 3 {
			return dirName[:3]
		}
		if dirName != "" {
			return dirName
		}
		return "att" // fallback for empty/separator-only input
	}

	var prefix strings.Builder
	for _, part := range parts {
		if part != "" {
			prefix.WriteByte(part[0])
		}
	}
	result := prefix.String()
	if result == "" {
		return "att" // fallback for edge cases like all-empty segments
	}
	return result
}

func randomSuffix(n int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b), nil
}
