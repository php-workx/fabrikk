package engine

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/php-workx/fabrikk/internal/state"
)

const maxSpecNormalizationSourceLineBytes = 10 << 20

type specNormalizationSourceBundle struct {
	Manifest     *state.SpecNormalizationSourceManifest
	ManifestHash string
	PromptInput  string
	TotalBytes   int64
}

type specNormalizationSourceSnapshot struct {
	Fingerprint      string
	ByteSize         int64
	LineCount        int
	LineNumberedText string
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
		snapshot, err := readSpecNormalizationSourceSnapshot(path)
		if err != nil {
			return nil, fmt.Errorf("read source spec %s: %w", path, err)
		}
		totalBytes += snapshot.ByteSize
		if maxBytes > 0 && totalBytes > maxBytes {
			return nil, fmt.Errorf("source bundle is %d bytes, above limit %d bytes; split the spec into smaller files or use explicit AT-* requirement IDs", totalBytes, maxBytes)
		}

		manifest.Sources = append(manifest.Sources, state.SourceManifestEntry{
			Path:             path,
			Fingerprint:      snapshot.Fingerprint,
			ByteSize:         snapshot.ByteSize,
			LineCount:        snapshot.LineCount,
			LineNumberedText: snapshot.LineNumberedText,
		})

		if prompt.Len() > 0 {
			prompt.WriteString("\n\n")
		}
		prompt.WriteString("## Source: ")
		prompt.WriteString(path)
		prompt.WriteString("\nFingerprint: ")
		prompt.WriteString(snapshot.Fingerprint)
		prompt.WriteString("\nByte size: ")
		fmt.Fprintf(&prompt, "%d", snapshot.ByteSize)
		prompt.WriteString("\nLine count: ")
		fmt.Fprintf(&prompt, "%d", snapshot.LineCount)
		prompt.WriteString("\n\n")
		prompt.WriteString(snapshot.LineNumberedText)
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

func readSpecNormalizationSourceSnapshot(path string) (specNormalizationSourceSnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return specNormalizationSourceSnapshot{}, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return specNormalizationSourceSnapshot{}, err
	}
	hasher := sha256.New()
	lineNumbered, lineCount, err := lineNumberSourceReader(io.TeeReader(f, hasher))
	if err != nil {
		return specNormalizationSourceSnapshot{}, err
	}
	return specNormalizationSourceSnapshot{
		Fingerprint:      hex.EncodeToString(hasher.Sum(nil)),
		ByteSize:         info.Size(),
		LineCount:        lineCount,
		LineNumberedText: lineNumbered,
	}, nil
}

func lineNumberSourceReader(r io.Reader) (numbered string, count int, err error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSpecNormalizationSourceLineBytes)
	var out strings.Builder
	line := 0
	for scanner.Scan() {
		line++
		if line > 1 {
			out.WriteString("\n")
		}
		fmt.Fprintf(&out, "%d | %s", line, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", 0, err
	}
	return out.String(), line, nil
}
