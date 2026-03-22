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
		return fmt.Errorf("usage: attest learn <content|query|handoff|list|gc|maintain|repair> [args]")
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
	case "maintain":
		return cmdLearnMaintain(store)
	case "repair":
		return cmdLearnRepair(store)
	default:
		// First arg is the content — add a learning.
		return cmdLearnAdd(store, args)
	}
}

type learnAddOpts struct {
	content    string
	tags       []string
	paths      []string
	category   learning.Category
	sourceTask string
	sourceRun  string
}

func parseLearnAddArgs(args []string) learnAddOpts {
	opts := learnAddOpts{content: args[0]}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case flagTag:
			if i+1 < len(args) {
				opts.tags = append(opts.tags, strings.Split(args[i+1], ",")...)
				i++
			}
		case "--category":
			if i+1 < len(args) {
				opts.category = learning.Category(args[i+1])
				i++
			}
		case "--path":
			if i+1 < len(args) {
				opts.paths = append(opts.paths, args[i+1])
				i++
			}
		case "--source-task":
			if i+1 < len(args) {
				opts.sourceTask = args[i+1]
				i++
			}
		case "--source-run":
			if i+1 < len(args) {
				opts.sourceRun = args[i+1]
				i++
			}
		}
	}
	if opts.category == "" {
		opts.category = learning.CategoryCodebase
	}
	return opts
}

// toRepoRelativePaths converts absolute paths to repo-relative form.
func toRepoRelativePaths(paths []string) []string {
	wd, wdErr := workDir()
	if wdErr != nil {
		return paths
	}
	for i, p := range paths {
		if filepath.IsAbs(p) {
			if rel, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(rel, "..") {
				paths[i] = rel
			}
		}
	}
	return paths
}

func cmdLearnAdd(store *learning.Store, args []string) error {
	opts := parseLearnAddArgs(args)

	// Auto-populate SourcePaths from task's OwnedPaths when --source-task is set.
	if opts.sourceTask != "" && len(opts.paths) == 0 {
		wd, wdErr := workDir()
		if wdErr == nil {
			taskStore := taskStoreForRun(wd, "")
			if task, taskErr := taskStore.ReadTask(opts.sourceTask); taskErr == nil {
				opts.paths = task.Scope.OwnedPaths
			}
		}
	}

	opts.paths = toRepoRelativePaths(opts.paths)

	// Use first line as summary if content is multi-line.
	summary := opts.content
	if idx := strings.IndexByte(opts.content, '\n'); idx > 0 {
		summary = opts.content[:idx]
	}
	if len(summary) > 100 {
		summary = summary[:100] + "..."
	}

	l := &learning.Learning{
		Tags:        opts.tags,
		Category:    opts.category,
		Content:     opts.content,
		Summary:     summary,
		SourceTask:  opts.sourceTask,
		SourceRun:   opts.sourceRun,
		SourcePaths: opts.paths,
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
		case "--min-effectiveness":
			if i+1 < len(args) {
				_, _ = fmt.Sscanf(args[i+1], "%f", &opts.MinEffectiveness)
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
		fmt.Printf("  [%s] (%s, eff=%.2f, attach=%d/%d) %s\n",
			l.ID, l.Category, l.Effectiveness(), l.AttachCount, l.SuccessCount, l.Summary)
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
		fmt.Printf("  %s  [%s] eff=%.2f  %s\n",
			l.CreatedAt.Format("2006-01-02"), l.ID, l.Effectiveness(), l.Summary)
	}

	// Show latest handoff if exists.
	h, hErr := store.LatestHandoff()
	if hErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read latest handoff: %v\n", hErr)
	}
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

func cmdLearnMaintain(store *learning.Store) error {
	report, err := store.Maintain(90 * 24 * time.Hour)
	if err != nil {
		return err
	}
	if report.Skipped {
		fmt.Println("Maintenance skipped (already running).")
		return nil
	}
	fmt.Println("Learning store maintenance:")
	fmt.Printf("  Merged:       %d\n", report.Merged)
	fmt.Printf("  Auto-expired: %d\n", report.AutoExpired)
	fmt.Printf("  GC:           %d removed\n", report.GCRemoved)
	if report.IndexRebuilt {
		fmt.Printf("  Index:        rebuilt\n")
	}
	return nil
}

func cmdLearnRepair(store *learning.Store) error {
	kept, dropped, err := store.Repair()
	if err != nil {
		return err
	}
	fmt.Printf("Repaired: kept %d learnings, dropped %d corrupt lines.\n", kept, dropped)
	return nil
}

func printContextBundle(bundle *learning.ContextBundle) {
	fmt.Printf("Context for %s (tokens: %d/%d):\n\n", bundle.TaskID, bundle.TokensUsed, bundle.TokenBudget)
	if len(bundle.Learnings) > 0 {
		fmt.Printf("Learnings (%d):\n", len(bundle.Learnings))
		for i := range bundle.Learnings {
			l := &bundle.Learnings[i]
			fmt.Printf("  [%s] (%s, eff=%.2f): %s\n", l.ID, l.Category, l.Effectiveness(), l.Summary)
			if l.Source != "" && l.Source != "manual" {
				fmt.Printf("         source: %s", l.Source)
				if l.SourceFinding != "" {
					fmt.Printf(", finding %s", l.SourceFinding)
				}
				if l.SourceRun != "" {
					fmt.Printf(", %s", l.SourceRun)
				}
				fmt.Println()
			}
		}
	} else {
		fmt.Println("No matching learnings.")
	}

	if bundle.Handoff != nil {
		fmt.Printf("\nSession handoff (%s ago):\n  %s\n",
			time.Since(bundle.Handoff.CreatedAt).Truncate(time.Minute), bundle.Handoff.Summary)
		for _, s := range bundle.Handoff.NextSteps {
			fmt.Printf("  Next: %s\n", s)
		}
	}
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

	// Assemble context bundle for this task.
	learnStore := learning.NewStore(filepath.Join(wd, ".attest", "learnings"))
	bundle, err := learnStore.AssembleContext(task.TaskID, task.Tags, task.Scope.OwnedPaths)
	if err != nil {
		return fmt.Errorf("assemble context: %w", err)
	}

	// Output.
	jsonOutput := false
	for _, a := range args {
		if a == flagJSON {
			jsonOutput = true
		}
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(bundle)
	}

	printContextBundle(bundle)
	return nil
}
