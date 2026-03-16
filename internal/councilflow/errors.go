package councilflow

import "errors"

// Sentinel errors for council pipeline error classification.
// Callers use errors.Is/errors.As to determine policy (fatal vs skip vs retry).
var (
	ErrBackendUnavailable      = errors.New("backend unavailable")
	ErrSubprocessFailed        = errors.New("subprocess failed")
	ErrReviewTimeout           = errors.New("review timeout")
	ErrInvalidReviewJSON       = errors.New("invalid review JSON")
	ErrEmptyReview             = errors.New("empty review")
	ErrPersonaGenerationFailed = errors.New("persona generation failed")
	ErrJudgeFailed             = errors.New("judge failed")
	ErrInvalidPersonaConfig    = errors.New("invalid persona config")
)
