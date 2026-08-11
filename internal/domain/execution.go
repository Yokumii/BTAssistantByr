package domain

// ExecutionMode controls whether a TopFree write plan needs manual approval.
type ExecutionMode string

const (
	// ExecutionModeRequestApproval requires explicit approval before each write plan.
	ExecutionModeRequestApproval ExecutionMode = "request_approval"
	// ExecutionModeYOLO executes write plans without prompting.
	ExecutionModeYOLO ExecutionMode = "yolo"
)

// Valid reports whether the execution mode has defined behavior.
func (mode ExecutionMode) Valid() bool {
	switch mode {
	case ExecutionModeRequestApproval, ExecutionModeYOLO:
		return true
	default:
		return false
	}
}
