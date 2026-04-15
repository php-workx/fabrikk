package ticket

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/internal/state"
)

func testTask() state.Task {
	now := time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)
	return state.Task{
		TaskID:         "task-slice-1",
		Slug:           "task_slice_1",
		Title:          "Implement auth module",
		TaskType:       "implementation",
		Tags:           []string{"auth", "high-risk"},
		CreatedAt:      now,
		UpdatedAt:      now,
		Order:          2,
		ETag:           "a1b2c3d4",
		LineageID:      "task-slice-1",
		RequirementIDs: []string{"REQ-AUTH-1"},
		DependsOn:      []string{"task-slice-0"},
		Scope: state.TaskScope{
			OwnedPaths:    []string{"internal/auth"},
			IsolationMode: "direct",
		},
		Priority:         1,
		RiskLevel:        "high",
		DefaultModel:     "sonnet",
		Status:           state.TaskPending,
		RequiredEvidence: []string{"quality_gate_pass"},
		ValidationChecks: []state.ValidationCheck{{Type: "command", Tool: "go", Args: []string{"test", "./..."}}},
		ImplementationDetail: state.ImplementationDetail{
			FilesToModify: []state.FileChange{{Path: "internal/auth/service.go", Change: "Add auth service", IsNew: true}},
			SymbolsToAdd:  []string{"func NewService() *Service"},
			SymbolsToUse:  []string{"validateToken at auth.go:42"},
			TestsToAdd:    []string{"TestNewService"},
		},
		ParentTaskID: "run-123",
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	task := testTask()

	data, err := MarshalTicket(&task)
	if err != nil {
		t.Fatalf("MarshalTicket: %v", err)
	}

	got, err := UnmarshalTicket(data)
	if err != nil {
		t.Fatalf("UnmarshalTicket: %v", err)
	}

	if got.TaskID != task.TaskID {
		t.Errorf("TaskID = %q, want %q", got.TaskID, task.TaskID)
	}
	if got.Title != task.Title {
		t.Errorf("Title = %q, want %q", got.Title, task.Title)
	}
	if got.Status != task.Status {
		t.Errorf("Status = %q, want %q", got.Status, task.Status)
	}
	if got.Priority != task.Priority {
		t.Errorf("Priority = %d, want %d", got.Priority, task.Priority)
	}
	if got.Order != task.Order {
		t.Errorf("Order = %d, want %d", got.Order, task.Order)
	}
	if got.ParentTaskID != task.ParentTaskID {
		t.Errorf("ParentTaskID = %q, want %q", got.ParentTaskID, task.ParentTaskID)
	}
	if len(got.DependsOn) != len(task.DependsOn) {
		t.Errorf("DependsOn len = %d, want %d", len(got.DependsOn), len(task.DependsOn))
	}
	if len(got.Scope.OwnedPaths) != len(task.Scope.OwnedPaths) {
		t.Errorf("OwnedPaths len = %d, want %d", len(got.Scope.OwnedPaths), len(task.Scope.OwnedPaths))
	}
	if len(got.ImplementationDetail.FilesToModify) != 1 {
		t.Fatalf("FilesToModify len = %d, want 1", len(got.ImplementationDetail.FilesToModify))
	}
	if got.ImplementationDetail.FilesToModify[0].Path != task.ImplementationDetail.FilesToModify[0].Path {
		t.Errorf("FilesToModify[0].Path = %q, want %q", got.ImplementationDetail.FilesToModify[0].Path, task.ImplementationDetail.FilesToModify[0].Path)
	}
	if len(got.ImplementationDetail.SymbolsToAdd) != 1 || got.ImplementationDetail.SymbolsToAdd[0] != task.ImplementationDetail.SymbolsToAdd[0] {
		t.Errorf("SymbolsToAdd = %v, want %v", got.ImplementationDetail.SymbolsToAdd, task.ImplementationDetail.SymbolsToAdd)
	}
	if len(got.ImplementationDetail.SymbolsToUse) != 1 || got.ImplementationDetail.SymbolsToUse[0] != task.ImplementationDetail.SymbolsToUse[0] {
		t.Errorf("SymbolsToUse = %v, want %v", got.ImplementationDetail.SymbolsToUse, task.ImplementationDetail.SymbolsToUse)
	}
	if len(got.ImplementationDetail.TestsToAdd) != 1 || got.ImplementationDetail.TestsToAdd[0] != task.ImplementationDetail.TestsToAdd[0] {
		t.Errorf("TestsToAdd = %v, want %v", got.ImplementationDetail.TestsToAdd, task.ImplementationDetail.TestsToAdd)
	}
}

func TestMarshalTicketUsesSnakeCaseForNestedYAML(t *testing.T) {
	task := testTask()

	data, err := MarshalTicket(&task)
	if err != nil {
		t.Fatalf("MarshalTicket: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"validation_checks:",
		"implementation_detail:",
		"files_to_modify:",
		"symbols_to_add:",
		"symbols_to_use:",
		"tests_to_add:",
		"is_new:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ticket YAML missing %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{
		"FilesToModify:",
		"SymbolsToAdd:",
		"SymbolsToUse:",
		"TestsToAdd:",
		"IsNew:",
	} {
		if strings.Contains(got, bad) {
			t.Fatalf("ticket YAML contains Go field name %q:\n%s", bad, got)
		}
	}
}

func TestStatusMapping(t *testing.T) {
	tests := []struct {
		attest state.TaskStatus
		wantTk string
	}{
		{state.TaskPending, "open"},
		{state.TaskClaimed, "in_progress"},
		{state.TaskImplementing, "in_progress"},
		{state.TaskVerifying, "in_progress"},
		{state.TaskUnderReview, "in_progress"},
		{state.TaskRepairPending, "open"},
		{state.TaskBlocked, "open"},
		{state.TaskDone, "closed"},
		{state.TaskFailed, "closed"},
	}
	for _, tt := range tests {
		got := StatusToTicket(tt.attest)
		if got != tt.wantTk {
			t.Errorf("StatusToTicket(%s) = %q, want %q", tt.attest, got, tt.wantTk)
		}
	}
}

func TestStatusFromTicketPrefersExtendedStatus(t *testing.T) {
	got := StatusFromTicket("open", "implementing")
	if got != state.TaskImplementing {
		t.Errorf("StatusFromTicket(open, implementing) = %q, want implementing", got)
	}
}

func TestStatusFromTicketFallsBackToTkStatus(t *testing.T) {
	got := StatusFromTicket("in_progress", "")
	if got != state.TaskClaimed {
		t.Errorf("StatusFromTicket(in_progress, '') = %q, want claimed", got)
	}
}

func TestStatusFromTicketRejectsInvalidExtendedStatus(t *testing.T) {
	// Invalid extended_status should fall back to tk status mapping, not blindly cast.
	got := StatusFromTicket("closed", "typo_status")
	if got != state.TaskDone {
		t.Errorf("StatusFromTicket(closed, typo_status) = %q, want done (fallback to tk status)", got)
	}

	got = StatusFromTicket("open", "")
	if got != state.TaskPending {
		t.Errorf("StatusFromTicket(open, '') = %q, want pending", got)
	}
}

func TestMarshalContainsFrontmatterDelimiters(t *testing.T) {
	task := testTask()
	data, err := MarshalTicket(&task)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		t.Error("missing opening frontmatter delimiter")
	}
	if !strings.Contains(content, "\n---\n") {
		t.Error("missing closing frontmatter delimiter")
	}
	if !strings.Contains(content, "# Implement auth module") {
		t.Error("missing title heading")
	}
}

func TestMarshalContainsAttestFields(t *testing.T) {
	task := testTask()
	data, _ := MarshalTicket(&task)
	content := string(data)

	for _, field := range []string{"extended_status:", "requirement_ids:", "risk_level:", "owned_paths:"} {
		if !strings.Contains(content, field) {
			t.Errorf("missing field %q in output", field)
		}
	}
}

func TestMarshalTicketIncludesValidationSection(t *testing.T) {
	task := testTask()
	task.ValidationChecks = []state.ValidationCheck{{
		Type:    "command",
		Paths:   []string{"internal/ticket/format.go"},
		File:    "internal/ticket/format.go",
		Pattern: "ValidationChecks",
		Tool:    "go",
		Args:    []string{"test", "./internal/ticket"},
	}}

	data, err := MarshalTicket(&task)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "## Validation") {
		t.Fatal("missing validation section")
	}
	for _, want := range []string{"**command**", "tool=go", "args=[test, ./internal/ticket]", "file=internal/ticket/format.go", "pattern=`ValidationChecks`"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing validation detail %q", want)
		}
	}

	got, err := UnmarshalTicket(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ValidationChecks) != 1 {
		t.Fatalf("ValidationChecks len = %d, want 1", len(got.ValidationChecks))
	}
	if got.ValidationChecks[0].Tool != "go" {
		t.Fatalf("ValidationChecks[0].Tool = %q, want go", got.ValidationChecks[0].Tool)
	}
	if !slices.Equal(got.ValidationChecks[0].Args, []string{"test", "./internal/ticket"}) {
		t.Fatalf("ValidationChecks[0].Args = %q, want go test args", got.ValidationChecks[0].Args)
	}
}

func TestMarshalTicketIncludesReadOnlyOnlyScopeSection(t *testing.T) {
	task := testTask()
	task.Scope.OwnedPaths = nil
	task.Scope.ReadOnlyPaths = []string{"docs/specs"}

	data, err := MarshalTicket(&task)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "## Scope") {
		t.Fatal("missing scope section")
	}
	if strings.Contains(content, "Owned paths:") {
		t.Fatalf("unexpected owned paths line in scope section:\n%s", content)
	}
	if !strings.Contains(content, "Read-only paths: docs/specs") {
		t.Fatalf("missing read-only scope line:\n%s", content)
	}
}

func TestMarshalTicketIncludesImplementationDetailSection(t *testing.T) {
	task := testTask()

	data, err := MarshalTicket(&task)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "## Implementation Detail") {
		t.Fatal("missing implementation detail section")
	}
	for _, want := range []string{
		"- file `internal/auth/service.go`: Add auth service (new)",
		"- add symbol `func NewService() *Service`",
		"- use symbol `validateToken at auth.go:42`",
		"- add test `TestNewService`",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("missing implementation detail %q", want)
		}
	}

	got, err := UnmarshalTicket(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ImplementationDetail.FilesToModify) != 1 || !got.ImplementationDetail.FilesToModify[0].IsNew {
		t.Fatalf("FilesToModify = %+v, want one new file change", got.ImplementationDetail.FilesToModify)
	}
}

func TestMarshalTicketOmitsEmptyImplementationDetailSection(t *testing.T) {
	task := testTask()
	task.ImplementationDetail = state.ImplementationDetail{}

	data, err := MarshalTicket(&task)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "## Implementation Detail") {
		t.Fatal("implementation detail section should be omitted when detail is empty")
	}
}

func TestOrderZeroIsPreserved(t *testing.T) {
	task := testTask()
	task.Order = 0

	data, err := MarshalTicket(&task)
	if err != nil {
		t.Fatal(err)
	}

	got, err := UnmarshalTicket(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Order != 0 {
		t.Errorf("Order = %d, want 0 (must not be dropped by omitempty)", got.Order)
	}
}

func TestMalformedYAML(t *testing.T) {
	_, err := UnmarshalTicket([]byte("not yaml at all"))
	if err == nil {
		t.Fatal("should error on malformed input")
	}
}

func TestPreserveUnknownYAMLFields(t *testing.T) {
	// MarshalTicket does NOT preserve Extra (it builds fresh from TaskToFrontmatter).
	// But UpdateFrontmatter DOES preserve Extra by merging with the existing frontmatter.
	raw := []byte("---\nid: test\nstatus: open\npriority: 0\norder: 0\ncustom_field: hello_world\n---\n# Test\n")
	task, err := UnmarshalTicket(raw)
	if err != nil {
		t.Fatalf("UnmarshalTicket: %v", err)
	}

	// Round-trip through UpdateFrontmatter (the body-preserving path).
	updated, err := UpdateFrontmatter(raw, task)
	if err != nil {
		t.Fatalf("UpdateFrontmatter: %v", err)
	}
	content := string(updated)
	if !strings.Contains(content, "custom_field: hello_world") {
		t.Error("Extra field custom_field was lost during UpdateFrontmatter round-trip")
	}
}

func TestEmptyOptionalFields(t *testing.T) {
	task := state.Task{
		TaskID:   "minimal",
		Title:    "Minimal task",
		Status:   state.TaskPending,
		TaskType: "task",
	}
	data, err := MarshalTicket(&task)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	// Empty slices and empty strings should not appear as empty YAML entries.
	if strings.Contains(content, "requirement_ids: []") {
		t.Error("empty requirement_ids should be omitted")
	}
	if strings.Contains(content, "status_reason: \"\"") {
		t.Error("empty status_reason should be omitted")
	}
}

func TestMarshalTicketWithLearningContext(t *testing.T) {
	task := state.Task{
		TaskID: "task-test",
		Title:  "Test task",
		Status: state.TaskPending,
		LearningContext: []state.LearningRef{
			{ID: "lrn-abc", Category: "pattern", Summary: "Test learning"},
		},
	}

	data, err := MarshalTicket(&task)
	if err != nil {
		t.Fatalf("MarshalTicket: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "## Context from Learnings") {
		t.Error("missing '## Context from Learnings' section")
	}
	if !strings.Contains(content, "lrn-abc") {
		t.Error("missing learning ID 'lrn-abc'")
	}
	if !strings.Contains(content, "pattern") {
		t.Error("missing category 'pattern'")
	}
	if !strings.Contains(content, "Test learning") {
		t.Error("missing summary 'Test learning'")
	}
}

func TestUpdateFrontmatterPreservesLearningFields(t *testing.T) {
	task := state.Task{
		TaskID:             "task-learn",
		Title:              "Learning fields test",
		Status:             state.TaskPending,
		FilesLikelyTouched: []string{"internal/engine/plan.go", "internal/engine/explore.go"},
		ValidationChecks: []state.ValidationCheck{{
			Type:  "files_exist",
			Paths: []string{"internal/engine/plan.go"},
		}},
		ImplementationDetail: state.ImplementationDetail{
			FilesToModify: []state.FileChange{{Path: "internal/engine/plan.go", Change: "Refine plan drafting"}},
			TestsToAdd:    []string{"TestDraftExecutionPlan"},
		},
		Intent:      "Implement the feature safely",
		Constraints: []string{"Do not modify shared state"},
		Warnings:    []string{"High complexity area"},
		LearningIDs: []string{"lrn-001", "lrn-002"},
	}

	data, err := MarshalTicket(&task)
	if err != nil {
		t.Fatalf("MarshalTicket: %v", err)
	}

	// Create an updated task with different status but same learning fields.
	updated := task
	updated.Status = state.TaskDone

	result, err := UpdateFrontmatter(data, &updated)
	if err != nil {
		t.Fatalf("UpdateFrontmatter: %v", err)
	}

	// Unmarshal to verify fields survived.
	got, err := UnmarshalTicket(result)
	if err != nil {
		t.Fatalf("UnmarshalTicket: %v", err)
	}

	if got.Intent != "Implement the feature safely" {
		t.Errorf("Intent = %q, want %q", got.Intent, "Implement the feature safely")
	}
	if len(got.Constraints) != 1 || got.Constraints[0] != "Do not modify shared state" {
		t.Errorf("Constraints = %v, want [Do not modify shared state]", got.Constraints)
	}
	if len(got.Warnings) != 1 || got.Warnings[0] != "High complexity area" {
		t.Errorf("Warnings = %v, want [High complexity area]", got.Warnings)
	}
	if len(got.LearningIDs) != 2 || got.LearningIDs[0] != "lrn-001" || got.LearningIDs[1] != "lrn-002" {
		t.Errorf("LearningIDs = %v, want [lrn-001 lrn-002]", got.LearningIDs)
	}
	if len(got.FilesLikelyTouched) != 2 || got.FilesLikelyTouched[0] != "internal/engine/plan.go" || got.FilesLikelyTouched[1] != "internal/engine/explore.go" {
		t.Errorf("FilesLikelyTouched = %v, want [internal/engine/plan.go internal/engine/explore.go]", got.FilesLikelyTouched)
	}
	if len(got.ValidationChecks) != 1 || got.ValidationChecks[0].Type != "files_exist" {
		t.Errorf("ValidationChecks = %v, want files_exist check", got.ValidationChecks)
	}
	if len(got.ImplementationDetail.FilesToModify) != 1 || got.ImplementationDetail.FilesToModify[0].Path != "internal/engine/plan.go" {
		t.Errorf("ImplementationDetail.FilesToModify = %v, want internal/engine/plan.go", got.ImplementationDetail.FilesToModify)
	}
	if len(got.ImplementationDetail.TestsToAdd) != 1 || got.ImplementationDetail.TestsToAdd[0] != "TestDraftExecutionPlan" {
		t.Errorf("ImplementationDetail.TestsToAdd = %v, want [TestDraftExecutionPlan]", got.ImplementationDetail.TestsToAdd)
	}
}

func TestNotesAppendFormat(t *testing.T) {
	original := "---\nid: test\nstatus: open\npriority: 0\norder: 0\n---\n# Test\n\n## Notes\n\n**2026-03-19T10:00:00Z**\n\nFirst note.\n"

	// Simulate appending a note.
	content := original
	if !strings.Contains(content, "## Notes") {
		content += "\n## Notes\n"
	}
	content += "\n**2026-03-19T11:00:00Z**\n\nSecond note.\n"

	if !strings.Contains(content, "First note.") {
		t.Error("first note lost")
	}
	if !strings.Contains(content, "Second note.") {
		t.Error("second note not appended")
	}
	if strings.Count(content, "## Notes") != 1 {
		t.Error("## Notes section duplicated")
	}
}

func TestUpdateFrontmatterPreservesBody(t *testing.T) {
	original := []byte("---\nid: test\nstatus: open\npriority: 0\norder: 0\n---\n# Test\n\nSome description.\n\n## Notes\n\n**2026-03-19T10:00:00Z**\n\nImportant note.\n")

	task := &state.Task{
		TaskID: "test",
		Title:  "Test",
		Status: state.TaskDone,
	}

	updated, err := UpdateFrontmatter(original, task)
	if err != nil {
		t.Fatalf("UpdateFrontmatter: %v", err)
	}

	content := string(updated)
	if !strings.Contains(content, "Important note.") {
		t.Error("body content was lost")
	}
	if !strings.Contains(content, "## Notes") {
		t.Error("Notes section was lost")
	}
	if !strings.Contains(content, "extended_status: done") {
		t.Error("extended_status not updated")
	}
}

func TestUpdateFrontmatterPreservesTkNativeFields(t *testing.T) {
	// Ticket with tk-native fields not in state.Task: links, assignee, external-ref.
	raw := []byte("---\nid: task-1\nstatus: open\npriority: 1\norder: 0\nassignee: alice\nexternal-ref: gh-42\nlinks:\n- task-2\n- task-3\ncreated: \"2026-03-19T10:00:00Z\"\n---\n# Task 1\n\nSome notes here.\n")

	task, err := UnmarshalTicket(raw)
	if err != nil {
		t.Fatalf("UnmarshalTicket: %v", err)
	}

	// Simulate an engine status update.
	task.Status = state.TaskDone

	updated, err := UpdateFrontmatter(raw, task)
	if err != nil {
		t.Fatalf("UpdateFrontmatter: %v", err)
	}

	content := string(updated)

	// tk-native fields must survive.
	if !strings.Contains(content, "assignee: alice") {
		t.Error("assignee was lost")
	}
	if !strings.Contains(content, "external-ref: gh-42") {
		t.Error("external-ref was lost")
	}
	if !strings.Contains(content, "task-2") || !strings.Contains(content, "task-3") {
		t.Error("links were lost")
	}
	// Body must survive.
	if !strings.Contains(content, "Some notes here.") {
		t.Error("body was lost")
	}
	// Status must be updated.
	if !strings.Contains(content, "extended_status: done") {
		t.Error("extended_status not updated")
	}
}
