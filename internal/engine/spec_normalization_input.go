package engine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/php-workx/fabrikk/internal/state"
)

type specNormalizationSourceBundle struct {
	Manifest     *state.SpecNormalizationSourceManifest
	ManifestHash string
	PromptInput  string
	TotalBytes   int64
}

func buildSpecNormalizationSourceBundle(runID string, specPaths []string, maxBytes int64) (*specNormalizationSourceBundle, error) {
	manifest := &state.SpecNormalizationSourceManifest{
		SchemaVersion: "0.1",
		RunID:         runID,
		Sources:       make([]state.SourceManifestEntry, 0, len(specPaths)),
	}

	var prompt strings.Builder
	var totalBytes int64
	for _, path := range specPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read source spec %s: %w", path, err)
		}
		totalBytes += int64(len(data))
		if maxBytes > 0 && totalBytes > maxBytes {
			return nil, fmt.Errorf("source bundle is %d bytes, above limit %d bytes; split the spec into smaller files or use explicit AT-* requirement IDs", totalBytes, maxBytes)
		}

		fingerprint, err := state.SHA256File(path)
		if err != nil {
			return nil, fmt.Errorf("hash source spec %s: %w", path, err)
		}
		lineNumbered, lineCount := lineNumberSource(data)

		manifest.Sources = append(manifest.Sources, state.SourceManifestEntry{
			Path:             path,
			Fingerprint:      fingerprint,
			ByteSize:         int64(len(data)),
			LineCount:        lineCount,
			LineNumberedText: lineNumbered,
		})

		if prompt.Len() > 0 {
			prompt.WriteString("\n\n")
		}
		prompt.WriteString("## Source: ")
		prompt.WriteString(path)
		prompt.WriteString("\nFingerprint: ")
		prompt.WriteString(fingerprint)
		prompt.WriteString("\nByte size: ")
		fmt.Fprintf(&prompt, "%d", len(data))
		prompt.WriteString("\nLine count: ")
		fmt.Fprintf(&prompt, "%d", lineCount)
		prompt.WriteString("\n\n")
		prompt.WriteString(lineNumbered)
	}

	manifestHash, err := hashSpecNormalizationSourceManifest(manifest)
	if err != nil {
		return nil, err
	}

	return &specNormalizationSourceBundle{
		Manifest:     manifest,
		ManifestHash: manifestHash,
		PromptInput:  prompt.String(),
		TotalBytes:   totalBytes,
	}, nil
}

func hashSpecNormalizationSourceManifest(manifest *state.SpecNormalizationSourceManifest) (string, error) {
	if manifest == nil {
		return "", fmt.Errorf("marshal source manifest: missing manifest")
	}
	metadata := *manifest
	metadata.Sources = make([]state.SourceManifestEntry, len(manifest.Sources))
	for i, source := range manifest.Sources {
		source.LineNumberedText = ""
		metadata.Sources[i] = source
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("marshal source manifest: %w", err)
	}
	return sha256Prefix + state.SHA256Bytes(data), nil
}

func lineNumberSource(data []byte) (numbered string, count int) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var out strings.Builder
	line := 0
	for scanner.Scan() {
		line++
		if line > 1 {
			out.WriteString("\n")
		}
		fmt.Fprintf(&out, "%d | %s", line, scanner.Text())
	}
	return out.String(), line
}
