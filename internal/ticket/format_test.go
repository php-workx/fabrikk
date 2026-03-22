package ticket

import (
	"strings"
	"testing"
	"time"

	"github.com/runger/attest/internal/state"
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
		ParentTaskID:     "run-123",
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

func TestStatusFromTicketPrefersAttestStatus(t *testing.T) {
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

func TestStatusFromTicketRejectsInvalidAttestStatus(t *testing.T) {
	// Invalid attest_status should fall back to tk status mapping, not blindly cast.
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

	for _, field := range []string{"attest_status:", "requirement_ids:", "risk_level:", "owned_paths:"} {
		if !strings.Contains(content, field) {
			t.Errorf("missing field %q in output", field)
		}
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
		TaskID:      "task-learn",
		Title:       "Learning fields test",
		Status:      state.TaskPending,
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
	if !strings.Contains(content, "attest_status: done") {
		t.Error("attest_status not updated")
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
	if !strings.Contains(content, "attest_status: done") {
		t.Error("attest_status not updated")
	}
}
