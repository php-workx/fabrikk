package engine

import (
	"testing"

	"github.com/php-workx/fabrikk/internal/state"
)

func TestDeriveTags(t *testing.T) {
	task := &state.Task{
		Scope: state.TaskScope{
			OwnedPaths: []string{"internal/compiler", "cmd/fabrikk"},
		},
		RequirementIDs: []string{"AT-FR-001", "AT-TS-002"},
		TaskType:       "implementation",
	}

	tags := task.DeriveTags()

	has := func(needle string) bool {
		for _, tag := range tags {
			if tag == needle {
				return true
			}
		}
		return false
	}

	if !has("compiler") {
		t.Error("missing tag 'compiler' from OwnedPaths")
	}
	if !has("internal") {
		t.Error("missing tag 'internal' from OwnedPaths")
	}
	if !has("fabrikk") {
		t.Error("missing tag 'fabrikk' from OwnedPaths")
	}
	if !has("cmd") {
		t.Error("missing tag 'cmd' from OwnedPaths")
	}
	if !has("functional") {
		t.Error("missing tag 'functional' from AT-FR prefix")
	}
	if !has("testing") {
		t.Error("missing tag 'testing' from AT-TS prefix")
	}
	if !has("implementation") {
		t.Error("missing tag 'implementation' from TaskType")
	}
}

func TestDeriveTagsEmpty(t *testing.T) {
	task := &state.Task{}
	tags := task.DeriveTags()
	if len(tags) != 0 {
		t.Errorf("expected empty tags for empty task, got %v", tags)
	}
}

func TestDeriveTagsSorted(t *testing.T) {
	task := &state.Task{
		Scope: state.TaskScope{
			OwnedPaths: []string{"z/a/m"},
		},
		RequirementIDs: []string{"AT-NFR-001"},
		TaskType:       "repair",
	}

	tags := task.DeriveTags()
	for i := 1; i < len(tags); i++ {
		if tags[i] < tags[i-1] {
			t.Errorf("tags not sorted: %v", tags)
			break
		}
	}
}
