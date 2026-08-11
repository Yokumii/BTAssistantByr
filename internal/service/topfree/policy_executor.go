package topfree

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

var (
	// ErrInvalidExecutionMode indicates that the configured write policy is unknown.
	ErrInvalidExecutionMode = errors.New("invalid TopFree execution mode")
	// ErrDelegateExecutorRequired indicates that no guarded executor was supplied.
	ErrDelegateExecutorRequired = errors.New("TopFree delegate executor is required")
	// ErrApproverRequired indicates that approval mode has no approval provider.
	ErrApproverRequired = errors.New("TopFree approver is required")
	// ErrExecutionDeclined indicates that the current plan was not manually approved.
	ErrExecutionDeclined = errors.New("TopFree execution was not approved")
)

// Approver decides whether one complete TopFree write plan may execute.
type Approver interface {
	Approve(context.Context, Plan) (bool, error)
}

// PolicyExecutor gates a concrete executor according to the configured execution mode.
type PolicyExecutor struct {
	mode     domain.ExecutionMode
	delegate PlanExecutor
	approver Approver
	logger   *slog.Logger
}

// SetLogger enables approval-policy logs. It must be called before Execute.
func (e *PolicyExecutor) SetLogger(logger *slog.Logger) {
	e.logger = logger
}

// NewPolicyExecutor creates a write gate around an existing plan executor.
func NewPolicyExecutor(
	mode domain.ExecutionMode,
	delegate PlanExecutor,
	approver Approver,
) (*PolicyExecutor, error) {
	if !mode.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidExecutionMode, mode)
	}
	if delegate == nil {
		return nil, ErrDelegateExecutorRequired
	}
	if mode == domain.ExecutionModeRequestApproval && approver == nil {
		return nil, ErrApproverRequired
	}
	return &PolicyExecutor{mode: mode, delegate: delegate, approver: approver}, nil
}

// Execute asks once for approval of all writes in the plan when configured to do so.
func (e *PolicyExecutor) Execute(ctx context.Context, plan Plan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.mode == domain.ExecutionModeYOLO {
		if e.logger != nil && planHasWrites(plan) {
			e.logger.WarnContext(ctx, "YOLO mode executing TopFree writes without manual approval")
		}
		return e.delegate.Execute(ctx, plan)
	}

	if _, err := prepareExecutionPlan(plan); err != nil {
		return err
	}
	if !planHasWrites(plan) {
		return e.delegate.Execute(ctx, plan)
	}
	if e.logger != nil {
		e.logger.InfoContext(ctx, "waiting for manual approval of TopFree writes")
	}
	approved, err := e.approver.Approve(ctx, plan)
	if err != nil {
		return fmt.Errorf("request TopFree execution approval: %w", err)
	}
	if !approved {
		if e.logger != nil {
			e.logger.WarnContext(ctx, "TopFree writes declined by user")
		}
		return ErrExecutionDeclined
	}
	if e.logger != nil {
		e.logger.InfoContext(ctx, "TopFree writes approved by user")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.delegate.Execute(ctx, plan)
}

func planHasWrites(plan Plan) bool {
	return len(plan.TagAsManaged) > 0 ||
		len(plan.UnmarkForDeletion) > 0 ||
		len(plan.MarkForDeletion) > 0 ||
		len(plan.Downloads) > 0
}
