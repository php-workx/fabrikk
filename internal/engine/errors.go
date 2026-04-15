package engine

import "fmt"

// HumanInputRequiredError is returned when an operation requires human input
// for items that fall under an "ask_first" boundary (spec §2.6).
type HumanInputRequiredError struct {
	Items []string
}

func (e *HumanInputRequiredError) Error() string {
	return fmt.Sprintf("human input required for %d items", len(e.Items))
}
