package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/php-workx/fabrikk/internal/learning"
	"github.com/php-workx/fabrikk/internal/state"
)

const (
	flagTag   = "--tag"
	flagJSON  = "--json"
	flagLimit = "--limit"
)

func cmdLearn(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: fabrikk learn <content|query|search|handoff|list|gc|maintain|repair> [args]")
	}

	wd, err := workDir()
	if err != nil {
		return err
	}
	store := newLearningStore(wd)

	switch args[0] {
	case "search":
		return cmdLearnSearch(store, args[1:])
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

// toRepoRelativePaths converts paths to repo-relative form.
// Returns error if any path resolves outside the repository.
func toRepoRelativePaths(paths []string) ([]string, error) {
	wd, wdErr := workDir()
	if wdErr != nil {
		return paths, nil //nolint:nilerr // can't resolve without working dir, pass through
	}
	for i, p := range paths {
		if !filepath.IsAbs(p) {
			paths[i] = filepath.ToSlash(filepath.Clean(p))
			continue
		}
		rel, err := filepath.Rel(wd, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("--path %q is outside the repository", p)
		}
		paths[i] = filepath.ToSlash(rel)
	}
	return paths, nil
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

	var pathErr error
	opts.paths, pathErr = toRepoRelativePaths(opts.paths)
	if pathErr != nil {
		return pathErr
	}

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

func cmdLearnSearch(store *learning.Store, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: fabrikk learn search <text> [--limit N] [--json]")
	}
	searchText := args[0]
	opts := learning.QueryOpts{
		SearchText: searchText,
		SortBy:     "effectiveness",
	}
	jsonOutput := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case flagLimit:
			if i+1 < len(args) {
				_, _ = fmt.Sscanf(args[i+1], "%d", &opts.Limit)
				i++
			}
		case flagJSON:
			jsonOutput = true
		}
	}

	results, err := store.Query(opts)
	if err != nil {
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}

	if len(results) == 0 {
		fmt.Printf("No learnings matching %q.\n", searchText)
		return nil
	}

	fmt.Printf("Search %q (%d results):\n", searchText, len(results))
	for i := range results {
		l := &results[i]
		fmt.Printf("  [%s] (%s, eff=%.2f) %s\n", l.ID, l.Category, l.Effectiveness(), l.Summary)
		if len(l.Tags) > 0 {
			fmt.Printf("         tags: %s\n", strings.Join(l.Tags, ", "))
		}
	}
	return nil
}

func parseQueryArgs(args []string) learning.QueryOpts {
	opts := learning.QueryOpts{SortBy: "effectiveness"}
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
		case "--search":
			if i+1 < len(args) {
				opts.SearchText = args[i+1]
				i++
			}
		case "--min-effectiveness":
			if i+1 < len(args) {
				_, _ = fmt.Sscanf(args[i+1], "%f", &opts.MinEffectiveness)
				i++
			}
		case flagLimit:
			if i+1 < len(args) {
				_, _ = fmt.Sscanf(args[i+1], "%d", &opts.Limit)
				i++
			}
		case "--sort":
			if i+1 < len(args) {
				opts.SortBy = args[i+1]
				i++
			}
		}
	}
	return opts
}

func cmdLearnQuery(store *learning.Store, args []string) error {
	opts := parseQueryArgs(args)
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
		return fmt.Errorf("usage: fabrikk learn handoff --summary \"...\" [--next \"...\"] [--run X] [--task X]")
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
	fmt.Printf("  Prevention:   %d compiled\n", report.PreventionCompiled)
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

	if len(bundle.PreventionChecks) > 0 {
		fmt.Printf("\nPrevention checks (%d):\n", len(bundle.PreventionChecks))
		for _, check := range bundle.PreventionChecks {
			// Show first line (the header) of each check.
			if idx := strings.Index(check, "\n# "); idx >= 0 {
				end := strings.Index(check[idx+3:], "\n")
				if end > 0 {
					fmt.Printf("  %s\n", check[idx+3:idx+3+end])
				}
			}
		}
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
	jsonOutput := false
	var positional []string
	for _, a := range args {
		if a == flagJSON {
			jsonOutput = true
			continue
		}
		positional = append(positional, a)
	}
	if len(positional) < 2 {
		return fmt.Errorf("usage: fabrikk context <run-id> <task-id> [--json]")
	}
	runID := positional[0]
	taskID := positional[1]

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
	learnStore := newLearningStore(wd)
	bundle, err := learnStore.AssembleContext(task.TaskID, task.DeriveTags(), task.Scope.OwnedPaths, task.Title)
	if err != nil {
		return fmt.Errorf("assemble context: %w", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(bundle)
	}

	printContextBundle(bundle)
	return nil
}
