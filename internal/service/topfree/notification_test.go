package topfree

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

type recordingNotifier struct {
	subjects []string
	bodies   []string
	err      error
	ctxError error
}

func (n *recordingNotifier) Notify(ctx context.Context, subject, text string) error {
	n.subjects = append(n.subjects, subject)
	n.bodies = append(n.bodies, text)
	n.ctxError = ctx.Err()
	return n.err
}

func notificationExecutor(t *testing.T, client *fakeMutationClient, notifier *recordingNotifier, bonus *fakeBonusTracker) *Executor {
	t.Helper()
	e, err := NewExecutor(&fakeDownloadSource{}, client, "/downloads", 1)
	if err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return fixtureTime }
	if err := e.SetNotifier(notifier, bonus, time.Second); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestNotificationReportsSuccessfulCleanupAndActualSpace(t *testing.T) {
	t.Parallel()
	client := &fakeMutationClient{freeSpaceSequence: []int64{30, 60, 59}}
	notifier := &recordingNotifier{}
	bonus := &fakeBonusTracker{}
	e := notificationExecutor(t, client, notifier, bonus)
	old := managedTorrent(2, 40, 1, ManagedTag)
	old.SavePath = "/downloads"
	plan := Plan{Downloads: []DownloadPlan{{Candidate: candidate(1, 40, 1, 1), DeleteBefore: []domain.ManagedTorrent{old}}}}
	if err := e.Execute(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(notifier.bodies) != 1 || bonus.accountCalls != 2 {
		t.Fatalf("emails = %d, bonus reads = %d", len(notifier.bodies), bonus.accountCalls)
	}
	wantEvents := []string{"freeSpace", "reannounce", "pause", "delete", "freeSpace", "add", "freeSpace"}
	if !reflect.DeepEqual(client.events, wantEvents) {
		t.Fatalf("events = %v; want %v", client.events, wantEvents)
	}
	for _, text := range []string{"执行完成", "阶段：completed", "已确认成功的写请求：4", "delete_with_files：请求返回成功", "add：请求返回成功", "(30 bytes)", "(60 bytes)", "(59 bytes)", "未启用 bonus", "A=1.0000"} {
		if !strings.Contains(notifier.bodies[0], text) {
			t.Errorf("summary missing %q", text)
		}
	}
	for _, secret := range []string{"passkey", fixtureDownloadURL("1")} {
		if strings.Contains(notifier.bodies[0], secret) {
			t.Fatal("summary leaked download credentials")
		}
	}
}

func TestNotificationReportsPartialExecutionWithoutReplay(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("add failed with private error details")
	client := &fakeMutationClient{failOperation: "add", err: wantErr}
	notifier := &recordingNotifier{err: errors.New("email delivery failed")}
	e := notificationExecutor(t, client, notifier, &fakeBonusTracker{})
	var logs bytes.Buffer
	e.SetLogger(slog.New(slog.NewTextHandler(&logs, nil)))
	plan := Plan{
		Bonus:     &BonusEstimate{ObservedAt: fixtureTime, ValidUntil: fixtureTime.Add(time.Minute), BeforePerHour: 2, AfterPerHour: 3, NetPerHour: 1},
		Downloads: []DownloadPlan{{Candidate: candidate(1, 10, 1, 1), DeleteBefore: []domain.ManagedTorrent{managedTorrent(2, 20, 1, ManagedTag)}}},
	}
	if err := e.Execute(context.Background(), plan); !errors.Is(err, wantErr) {
		t.Fatalf("Execute() = %v, want original execution error", err)
	}
	if len(client.calls) != 4 || len(notifier.bodies) != 1 {
		t.Fatal("notification failure replayed writes or summaries")
	}
	for _, text := range []string{"执行中止", "阶段：add_downloads", "已确认成功的写请求：3", "delete_with_files：请求返回成功", "add：请求未确认成功", "不代表已完成部分的收益", "净变化：+1.0000"} {
		if !strings.Contains(notifier.bodies[0], text) {
			t.Errorf("summary missing %q", text)
		}
	}
	if strings.Contains(notifier.bodies[0], wantErr.Error()) {
		t.Fatal("summary included raw external error")
	}
	if !strings.Contains(logs.String(), "execution notification failed") {
		t.Fatal("notification failure was swallowed")
	}
}

func TestNotificationFailurePreservesSuccessfulExecution(t *testing.T) {
	t.Parallel()
	client := &fakeMutationClient{}
	notifier := &recordingNotifier{err: errors.New("Resend unavailable")}
	e := notificationExecutor(t, client, notifier, &fakeBonusTracker{})
	if err := e.Execute(context.Background(), Plan{TagAsManaged: hashes(1)}); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 1 || len(notifier.bodies) != 1 {
		t.Fatal("unexpected write or notification count")
	}
}

func TestNotificationSkippedBeforeAnyWriteAttempt(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		plan    Plan
		decline bool
	}{
		{"no writes", Plan{}, false},
		{"declined", Plan{TagAsManaged: hashes(1)}, true},
		{"invalid plan", Plan{TagAsManaged: []domain.TorrentHash{"invalid"}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeMutationClient{}
			notifier := &recordingNotifier{}
			bonus := &fakeBonusTracker{}
			e := notificationExecutor(t, client, notifier, bonus)
			if test.decline {
				policy, err := NewPolicyExecutor(domain.ExecutionModeRequestApproval, e, &recordingApprover{})
				if err != nil {
					t.Fatal(err)
				}
				if err := policy.Execute(context.Background(), test.plan); !errors.Is(err, ErrExecutionDeclined) {
					t.Fatal(err)
				}
			} else {
				_ = e.Execute(context.Background(), test.plan)
			}
			if len(client.calls) != 0 || len(notifier.bodies) != 0 || bonus.accountCalls != 0 || client.freeSpaceCalls != 0 {
				t.Fatal("non-writing cycle performed notification reads or delivery")
			}
		})
	}
}

func TestNotificationUnavailableDataDoesNotBecomeZero(t *testing.T) {
	t.Parallel()
	client := &fakeMutationClient{freeSpaceErr: errors.New("disk read failed")}
	notifier := &recordingNotifier{}
	e := notificationExecutor(t, client, notifier, &fakeBonusTracker{err: errors.New("BYR read failed")})
	if err := e.Execute(context.Background(), Plan{MarkForDeletion: hashes(1)}); err != nil {
		t.Fatal(err)
	}
	if len(notifier.bodies) != 1 {
		t.Fatal("missing notification")
	}
	body := notifier.bodies[0]
	if strings.Count(body, "数据不可用") != 4 || strings.Contains(body, "A=0.0000") || strings.Contains(body, "free=0.000") {
		t.Fatal("unavailable readings were replaced with zero")
	}
}

func TestNotificationOnFirstFailedWriteAndCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notifier := &recordingNotifier{}
	client := &fakeMutationClient{failOperation: "addTags", err: context.Canceled}
	e := notificationExecutor(t, client, notifier, &fakeBonusTracker{})
	// Simulate cancellation during the write, after its pre-write observations.
	e.client = &cancelingMutationClient{fakeMutationClient: client, cancel: cancel}
	if err := e.Execute(ctx, Plan{TagAsManaged: hashes(1)}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(notifier.bodies) != 1 || notifier.ctxError != nil || len(client.calls) != 1 {
		t.Fatal("cancellation suppressed summary or repeated writes")
	}
	if !strings.Contains(notifier.bodies[0], "已确认成功的写请求：0") || !strings.Contains(notifier.bodies[0], "远端可能已有副作用") {
		t.Fatal("first failed request was reported as unattempted or successful")
	}
}

type cancelingMutationClient struct {
	*fakeMutationClient
	cancel context.CancelFunc
}

func (c *cancelingMutationClient) AddTags(ctx context.Context, hashes []domain.TorrentHash, tags []string) error {
	c.cancel()
	return c.fakeMutationClient.AddTags(ctx, hashes, tags)
}

func TestNotificationDoesNotCompareDifferentDiskPaths(t *testing.T) {
	t.Parallel()
	client := &fakeMutationClient{freeSpacePaths: []string{"/downloads", "/other-disk"}}
	notifier := &recordingNotifier{}
	e := notificationExecutor(t, client, notifier, &fakeBonusTracker{})
	if err := e.Execute(context.Background(), Plan{TagAsManaged: hashes(1)}); err != nil {
		t.Fatal(err)
	}
	body := notifier.bodies[0]
	if !strings.Contains(body, "路径已变化") || strings.Contains(body, "本轮实测可用空间变化") {
		t.Fatal("summary compared capacity from different disks")
	}
}

func TestNotificationReportsExpiredDeletionWithoutDownload(t *testing.T) {
	t.Parallel()
	client := &fakeMutationClient{freeSpaceSequence: []int64{30, 40}}
	notifier := &recordingNotifier{}
	e := notificationExecutor(t, client, notifier, &fakeBonusTracker{})
	old := managedTorrent(2, 20, 1, ManagedTag)
	old.CompletedBytes = 10
	old.AmountLeftBytes = 10
	if err := e.Execute(context.Background(), Plan{RemoveExpired: []domain.ManagedTorrent{old}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.events, []string{"freeSpace", "pause", "delete", "freeSpace"}) || len(notifier.bodies) != 1 {
		t.Fatal("expiry cleanup without an addition lost its space observations or notification")
	}
	if !strings.Contains(notifier.bodies[0], "(30 bytes)") || !strings.Contains(notifier.bodies[0], "(40 bytes)") {
		t.Fatal("missing before/after space for expiry cleanup")
	}
}

func TestNotificationObservationsCannotExtendApprovedBonusValidity(t *testing.T) {
	t.Parallel()
	client := &fakeMutationClient{}
	notifier := &recordingNotifier{}
	e := notificationExecutor(t, client, notifier, &fakeBonusTracker{})
	now := fixtureTime
	e.now = func() time.Time { return now }
	e.notificationBonus = &advancingNotificationBonus{advance: func() { now = fixtureTime.Add(time.Minute) }}
	plan := Plan{
		Bonus:        &BonusEstimate{ObservedAt: fixtureTime, ValidUntil: fixtureTime.Add(time.Minute)},
		TagAsManaged: hashes(1),
	}
	if err := e.Execute(context.Background(), plan); !errors.Is(err, port.ErrInvalidTrackerSnapshot) {
		t.Fatalf("Execute() = %v, want expired plan", err)
	}
	if len(client.calls) != 0 || len(notifier.bodies) != 0 {
		t.Fatal("notification reads allowed expired writes or sent a summary without a write attempt")
	}
}

type advancingNotificationBonus struct {
	fakeBonusTracker
	advance func()
}

func (s *advancingNotificationBonus) BonusAccount(ctx context.Context) (domain.BonusAccount, error) {
	s.advance()
	return s.fakeBonusTracker.BonusAccount(ctx)
}
