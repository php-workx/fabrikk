package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/runger/attest/internal/learning"
	"github.com/runger/attest/internal/state"
)

const (
	flagTag  = "--tag"
	flagJSON = "--json"
)

func cmdLearn(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: attest learn <content|query|handoff|list|gc> [args]")
	}

	wd, err := workDir()
	if err != nil {
		return err
	}
	store := learning.NewStore(filepath.Join(wd, ".attest", "learnings"))

	switch args[0] {
	case "query":
		return cmdLearnQuery(store, args[1:])
	case "handoff":
		return cmdLearnHandoff(store, args[1:])
	case "list":
		return cmdLearnList(store)
	case "gc":
		return cmdLearnGC(store)
	default:
		// First arg is the content — add a learning.
		return cmdLearnAdd(store, args)
	}
}

func cmdLearnAdd(store *learning.Store, args []string) error {
	content := args[0]
	var tags []string
	var category learning.Category

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case flagTag:
			if i+1 < len(args) {
				tags = append(tags, strings.Split(args[i+1], ",")...)
				i++
			}
		case "--category":
			if i+1 < len(args) {
				category = learning.Category(args[i+1])
				i++
			}
		case "--source-task":
			// Accepted but not yet wired (engine enrichment handles this).
			i++
		case "--source-run":
			i++
		}
	}

	if category == "" {
		category = learning.CategoryCodebase
	}

	// Use first line as summary if content is multi-line.
	summary := content
	if idx := strings.IndexByte(content, '\n'); idx > 0 {
		summary = content[:idx]
	}
	if len(summary) > 100 {
		summary = summary[:100] + "..."
	}

	l := &learning.Learning{
		Tags:     tags,
		Category: category,
		Content:  content,
		Summary:  summary,
	}
	if err := store.Add(l); err != nil {
		return err
	}
	fmt.Printf("Learning added: %s\n", l.ID)
	fmt.Printf("  Tags: %s\n", strings.Join(l.Tags, ", "))
	fmt.Printf("  Category: %s\n", l.Category)
	fmt.Printf("  Summary: %s\n", l.Summary)
	return nil
}

func cmdLearnQuery(store *learning.Store, args []string) error {
	opts := learning.QueryOpts{SortBy: "utility"}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case flagTag:
			if i+1 < len(args) {
				opts.Tags = append(opts.Tags, strings.Split(args[i+1], ",")...)
				i++
			}
		case "--category":
			if i+1 < len(args) {
				opts.Category = learning.Category(args[i+1])
				i++
			}
		case "--path":
			if i+1 < len(args) {
				opts.Paths = append(opts.Paths, args[i+1])
				i++
			}
		case "--min-utility":
			if i+1 < len(args) {
				_, _ = fmt.Sscanf(args[i+1], "%f", &opts.MinUtility)
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				_, _ = fmt.Sscanf(args[i+1], "%d", &opts.Limit)
				i++
			}
		case "--sort":
			if i+1 < len(args) {
				opts.SortBy = args[i+1]
				i++
			}
		case flagJSON:
			// Handled after query.
		}
	}

	results, err := store.Query(opts)
	if err != nil {
		return err
	}

	// Check for --json flag.
	jsonOutput := false
	for _, a := range args {
		if a == flagJSON {
			jsonOutput = true
		}
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}

	if len(results) == 0 {
		fmt.Println("No learnings found.")
		return nil
	}

	fmt.Printf("Learnings (%d):\n", len(results))
	for i := range results {
		l := &results[i]
		fmt.Printf("  [%s] (%s, utility=%.2f, cited=%d) %s\n",
			l.ID, l.Category, l.Utility, l.CitedCount, l.Summary)
		if len(l.Tags) > 0 {
			fmt.Printf("         tags: %s\n", strings.Join(l.Tags, ", "))
		}
	}
	return nil
}

func cmdLearnHandoff(store *learning.Store, args []string) error {
	h := &learning.SessionHandoff{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--run":
			if i+1 < len(args) {
				h.RunID = args[i+1]
				i++
			}
		case "--task":
			if i+1 < len(args) {
				h.TaskID = args[i+1]
				i++
			}
		case "--summary":
			if i+1 < len(args) {
				h.Summary = args[i+1]
				i++
			}
		case "--next":
			if i+1 < len(args) {
				h.NextSteps = append(h.NextSteps, args[i+1])
				i++
			}
		case "--question":
			if i+1 < len(args) {
				h.OpenQuestions = append(h.OpenQuestions, args[i+1])
				i++
			}
		}
	}

	if h.Summary == "" {
		return fmt.Errorf("usage: attest learn handoff --summary \"...\" [--next \"...\"] [--run X] [--task X]")
	}

	if err := store.WriteHandoff(h); err != nil {
		return err
	}
	fmt.Printf("Handoff saved: %s\n", h.ID)
	if h.RunID != "" {
		fmt.Printf("  Run: %s\n", h.RunID)
	}
	if len(h.NextSteps) > 0 {
		fmt.Println("  Next steps:")
		for _, s := range h.NextSteps {
			fmt.Printf("    - %s\n", s)
		}
	}
	return nil
}

func cmdLearnList(store *learning.Store) error {
	results, err := store.Query(learning.QueryOpts{SortBy: "created_at"})
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Println("No learnings.")
		return nil
	}
	fmt.Printf("All learnings (%d):\n", len(results))
	for i := range results {
		l := &results[i]
		fmt.Printf("  %s  [%s] utility=%.2f  %s\n",
			l.CreatedAt.Format("2006-01-02"), l.ID, l.Utility, l.Summary)
	}

	// Show latest handoff if exists.
	h, _ := store.LatestHandoff()
	if h != nil {
		age := time.Since(h.CreatedAt).Truncate(time.Minute)
		fmt.Printf("\nLatest handoff (%s ago): %s\n", age, h.Summary)
	}
	return nil
}

func cmdLearnGC(store *learning.Store) error {
	removed, err := store.GarbageCollect(90 * 24 * time.Hour) // 90 days
	if err != nil {
		return err
	}
	fmt.Printf("Garbage collected: %d expired/superseded learnings removed.\n", removed)
	return nil
}

func cmdContext(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: attest context <run-id> <task-id>")
	}
	runID := args[0]
	taskID := args[1]

	wd, err := workDir()
	if err != nil {
		return err
	}

	store := taskStoreForRun(wd, runID)
	tasks, err := store.ReadTasks(runID)
	if err != nil {
		return fmt.Errorf("read tasks: %w", err)
	}

	var task *state.Task
	for i := range tasks {
		if tasks[i].TaskID == taskID {
			task = &tasks[i]
			break
		}
	}
	if task == nil {
		return fmt.Errorf("task %s not found in run %s", taskID, runID)
	}

	// Query learnings matching this task.
	learnStore := learning.NewStore(filepath.Join(wd, ".attest", "learnings"))
	learnings, _ := learnStore.Query(learning.QueryOpts{
		Tags:       task.Tags,
		Paths:      task.Scope.OwnedPaths,
		MinUtility: 0.1,
		Limit:      8,
		SortBy:     "utility",
	})

	// Get latest handoff.
	handoff, _ := learnStore.LatestHandoff()

	// Output.
	jsonOutput := false
	for _, a := range args {
		if a == flagJSON {
			jsonOutput = true
		}
	}

	if jsonOutput {
		out := map[string]interface{}{
			"task_id":    task.TaskID,
			"learnings":  learnings,
			"handoff":    handoff,
			"tokens_est": estimateTokens(learnings),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	fmt.Printf("Context for %s:\n\n", task.TaskID)
	if len(learnings) > 0 {
		fmt.Printf("Learnings (%d):\n", len(learnings))
		for i := range learnings {
			l := &learnings[i]
			fmt.Printf("  [%s] (%s, utility=%.2f): %s\n", l.ID, l.Category, l.Utility, l.Summary)
		}
	} else {
		fmt.Println("No matching learnings.")
	}

	if handoff != nil && time.Since(handoff.CreatedAt) < 24*time.Hour {
		fmt.Printf("\nSession handoff (%s ago):\n  %s\n",
			time.Since(handoff.CreatedAt).Truncate(time.Minute), handoff.Summary)
		for _, s := range handoff.NextSteps {
			fmt.Printf("  Next: %s\n", s)
		}
	}
	return nil
}

func estimateTokens(learnings []learning.Learning) int {
	total := 0
	for i := range learnings {
		total += len(learnings[i].Content) / 4
	}
	return total
}
