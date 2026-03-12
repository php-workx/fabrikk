package state_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/runger/attest/internal/state"
)

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	data := []byte(`{"key": "value"}`)
	if err := state.WriteAtomic(path, data); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestWriteAtomicCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "test.json")

	if err := state.WriteAtomic(path, []byte("ok")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestWriteAndReadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")

	type testData struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	want := testData{Name: "test", Count: 42}
	if err := state.WriteJSON(path, want); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var got testData
	if err := state.ReadJSON(path, &got); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSHA256Bytes(t *testing.T) {
	hash := state.SHA256Bytes([]byte("hello"))
	if len(hash) != 64 {
		t.Errorf("expected 64-char hex hash, got %d chars", len(hash))
	}

	// Same input should produce same hash.
	hash2 := state.SHA256Bytes([]byte("hello"))
	if hash != hash2 {
		t.Error("same input produced different hashes")
	}

	// Different input should produce different hash.
	hash3 := state.SHA256Bytes([]byte("world"))
	if hash == hash3 {
		t.Error("different inputs produced same hash")
	}
}
