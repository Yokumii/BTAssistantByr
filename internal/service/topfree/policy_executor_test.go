package topfree

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

func TestPolicyExecutorYOLOExecutesWithoutApproval(t *testing.T) {
	t.Parallel()

	delegate := &recordingExecutor{}
	executor, err := NewPolicyExecutor(domain.ExecutionModeYOLO, delegate, nil)
	if err != nil {
		t.Fatalf("NewPolicyExecutor() error = %v", err)
	}
	plan := Plan{MarkForDeletion: hashes(1)}
	if err := executor.Execute(context.Background(), plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !reflect.DeepEqual(delegate.plans, []Plan{plan}) {
		t.Errorf("delegate plans = %#v", delegate.plans)
	}
}

func TestPolicyExecutorRequiresApprovalBeforeWrites(t *testing.T) {
	t.Parallel()

	plan := Plan{Downloads: []DownloadPlan{{Candidate: candidate(1, 10, 1, 1)}}}
	tests := []struct {
		name      string
		approved  bool
		wantErr   error
		wantCalls int
	}{
		{name: "approved", approved: true, wantCalls: 1},
		{name: "declined", approved: false, wantErr: ErrExecutionDeclined},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			delegate := &recordingExecutor{}
			approver := &recordingApprover{approved: test.approved}
			executor, err := NewPolicyExecutor(domain.ExecutionModeRequestApproval, delegate, approver)
			if err != nil {
				t.Fatalf("NewPolicyExecutor() error = %v", err)
			}
			err = executor.Execute(context.Background(), plan)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Execute() error = %v, want %v", err, test.wantErr)
			}
			if approver.calls != 1 || !reflect.DeepEqual(approver.plan, plan) {
				t.Errorf("approver calls = %d, plan = %#v", approver.calls, approver.plan)
			}
			if len(delegate.plans) != test.wantCalls {
				t.Errorf("delegate calls = %d, want %d", len(delegate.plans), test.wantCalls)
			}
		})
	}
}

func TestPolicyExecutorSkipsApprovalWithoutWrites(t *testing.T) {
	t.Parallel()

	delegate := &recordingExecutor{}
	approver := &recordingApprover{approved: false}
	executor, err := NewPolicyExecutor(domain.ExecutionModeRequestApproval, delegate, approver)
	if err != nil {
		t.Fatalf("NewPolicyExecutor() error = %v", err)
	}
	plan := Plan{Skipped: []SkippedCandidate{{Reason: SkipInsufficientSpace}}}
	if err := executor.Execute(context.Background(), plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if approver.calls != 0 || len(delegate.plans) != 1 {
		t.Errorf("approver calls = %d, delegate calls = %d", approver.calls, len(delegate.plans))
	}
}

func TestPolicyExecutorStopsOnApprovalError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("terminal unavailable")
	delegate := &recordingExecutor{}
	approver := &recordingApprover{err: wantErr}
	executor, err := NewPolicyExecutor(domain.ExecutionModeRequestApproval, delegate, approver)
	if err != nil {
		t.Fatalf("NewPolicyExecutor() error = %v", err)
	}
	err = executor.Execute(context.Background(), Plan{MarkForDeletion: hashes(1)})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want approval error", err)
	}
	if len(delegate.plans) != 0 {
		t.Error("delegate executed after approval error")
	}
}

func TestPolicyExecutorRejectsUnsafePlanBeforeApproval(t *testing.T) {
	t.Parallel()

	delegate := &recordingExecutor{}
	approver := &recordingApprover{approved: true}
	executor, err := NewPolicyExecutor(domain.ExecutionModeRequestApproval, delegate, approver)
	if err != nil {
		t.Fatalf("NewPolicyExecutor() error = %v", err)
	}
	err = executor.Execute(context.Background(), Plan{MarkForDeletion: []domain.TorrentHash{"all"}})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("Execute() error = %v, want ErrInvalidPlan", err)
	}
	if approver.calls != 0 || len(delegate.plans) != 0 {
		t.Error("unsafe plan reached approval or execution")
	}
}

func TestNewPolicyExecutorValidatesConfiguration(t *testing.T) {
	t.Parallel()

	delegate := &recordingExecutor{}
	approver := &recordingApprover{}
	if _, err := NewPolicyExecutor("unknown", delegate, approver); !errors.Is(err, ErrInvalidExecutionMode) {
		t.Errorf("invalid mode error = %v", err)
	}
	if _, err := NewPolicyExecutor(domain.ExecutionModeYOLO, nil, nil); !errors.Is(err, ErrDelegateExecutorRequired) {
		t.Errorf("nil delegate error = %v", err)
	}
	if _, err := NewPolicyExecutor(domain.ExecutionModeRequestApproval, delegate, nil); !errors.Is(err, ErrApproverRequired) {
		t.Errorf("nil approver error = %v", err)
	}
}

func TestTerminalApprover(t *testing.T) {
	t.Parallel()

	plan := Plan{
		MarkForDeletion:         hashes(1),
		ProjectedDiskUsageBytes: 3 * (1 << 30),
		Downloads: []DownloadPlan{{
			Candidate:    candidate(2, 2*(1<<30), 1, 1),
			DeleteBefore: []domain.ManagedTorrent{managedTorrent(3, 1<<30, 1, ManagedTag)},
		}},
	}
	var output bytes.Buffer
	approver, err := NewTerminalApprover(strings.NewReader(ApprovalToken+"\n"), &output)
	if err != nil {
		t.Fatalf("NewTerminalApprover() error = %v", err)
	}
	approved, err := approver.Approve(context.Background(), plan)
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if !approved {
		t.Fatal("Approve() = false, want true")
	}
	for _, expected := range []string{"删除种子及文件：1", "下载种子：1", "3.00 GiB", "输入 APPLY"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("approval output does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestTerminalApproverRejectsAnyOtherInput(t *testing.T) {
	t.Parallel()

	approver, err := NewTerminalApprover(strings.NewReader("yes\n"), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewTerminalApprover() error = %v", err)
	}
	approved, err := approver.Approve(context.Background(), Plan{})
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if approved {
		t.Fatal("Approve() = true for non-APPLY input")
	}
}

type recordingExecutor struct {
	plans []Plan
	err   error
}

func (executor *recordingExecutor) Execute(_ context.Context, plan Plan) error {
	executor.plans = append(executor.plans, plan)
	return executor.err
}

type recordingApprover struct {
	approved bool
	err      error
	calls    int
	plan     Plan
}

func (approver *recordingApprover) Approve(_ context.Context, plan Plan) (bool, error) {
	approver.calls++
	approver.plan = plan
	return approver.approved, approver.err
}
