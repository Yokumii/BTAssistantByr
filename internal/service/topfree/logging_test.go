package topfree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/netretry"
)

func TestPlanDetailsKeepCandidateAndCleanupFieldsNamed(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	queued := candidate(1, 131340099911, 21, 3)
	cleanup := managedTorrent(2, 50, 1, ManagedTag, DeleteTag)
	plan := Plan{
		TagAsManaged: hashes(3), UnmarkForDeletion: hashes(4), MarkForDeletion: hashes(5),
		Queued: []domain.Candidate{queued}, RemoveExpired: []domain.ManagedTorrent{cleanup},
		Downloads: []DownloadPlan{{Candidate: queued, DeleteBefore: []domain.ManagedTorrent{cleanup}}},
		Skipped:   []SkippedCandidate{{Candidate: queued, Reason: SkipInsufficientSpace, AvailableBytes: 17}},
	}
	logPlanDetails(context.Background(), logger, plan, false)
	records := decodeLogRecords(t, output.String())
	for _, message := range []string{"TopFree queued candidate", "TopFree download planned", "TopFree candidate skipped"} {
		record := logRecordWithMessage(t, records, message)
		for key, want := range map[string]any{"torrent_id": "1", "hash": string(queued.Hash), "name": queued.Name, "size_bytes": float64(queued.SizeBytes), "seeders": float64(21), "leechers": float64(3)} {
			if record[key] != want {
				t.Errorf("%s: %s = %v, want %v", message, key, record[key], want)
			}
		}
		if record["free_until"] == nil {
			t.Errorf("%s missing free_until", message)
		}
	}
	record := logRecordWithMessage(t, records, "TopFree candidate skipped")
	if record["reason"] != string(SkipInsufficientSpace) || record["available_bytes"] != float64(17) {
		t.Errorf("skip diagnostics = %#v", record)
	}
	record = logRecordWithMessage(t, records, "TopFree capacity cleanup planned")
	if record["hash"] != string(cleanup.Hash) || record["for_torrent_id"] != "1" || record["completed_bytes"] != float64(50) {
		t.Errorf("cleanup diagnostics = %#v", record)
	}
}

func TestCoordinatorLogsReasonAndIdentityWhenQueueIsBlocked(t *testing.T) {
	t.Parallel()
	for _, pending := range []bool{false, true} {
		name := "unfinished download"
		if pending {
			name = "pending client visibility"
		}
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			source := &fakeManagedSource{}
			coordinator, err := NewCoordinator(source, &fakePlanExecutor{}, 100, 0, 1, "/downloads")
			if err != nil {
				t.Fatal(err)
			}
			coordinator.now = func() time.Time { return fixtureTime }
			coordinator.SetLogger(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
			if pending {
				coordinator.queue.markSubmitted(torrentHash(1))
			} else {
				active := managedTorrent(1, 10, 1, ManagedTag)
				active.CompletedBytes = 5
				active.AmountLeftBytes = 5
				active.State = "downloading"
				source.torrents = []domain.ManagedTorrent{active}
			}
			plan, err := coordinator.RunOnce(context.Background(), []domain.Candidate{candidate(1, 10, 1, 1), candidate(2, 10, 21, 3)})
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Downloads) != 0 || len(plan.Queued) != 1 {
				t.Fatalf("unexpected plan: %+v", plan)
			}
			records := decodeLogRecords(t, output.String())
			record := logRecordWithMessage(t, records, "TopFree queue reconciled")
			if record["download_blocked"] != true {
				t.Errorf("queue did not expose blocking: %#v", record)
			}
			if pending {
				if record["pending_hash"] != string(torrentHash(1)) {
					t.Errorf("missing pending identity: %#v", record)
				}
				record = logRecordWithMessage(t, records, "submitted TopFree torrent blocks next download")
				if record["reason"] != "awaiting_client_visibility" || record["hash"] != string(torrentHash(1)) {
					t.Errorf("missing pending reason: %#v", record)
				}
			} else {
				record = logRecordWithMessage(t, records, "unfinished current TopFree torrent blocks next download")
				if record["reason"] != "unfinished_current_topfree" || record["hash"] != string(torrentHash(1)) || record["amount_left_bytes"] != float64(5) {
					t.Errorf("missing active identity: %#v", record)
				}
			}
		})
	}
}

func TestExecutorFailureLogsPhaseAndConfirmedRequests(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		operation string
		confirmed int
		phase     string
		transient bool
	}{
		{"reannounce", 0, "cleanup_downloads", false},
		{"pause", 1, "cleanup_downloads", false},
		{"delete", 2, "cleanup_downloads", false},
		{"add", 3, "add_downloads", false},
		{"pause", 1, "cleanup_downloads", true},
	} {
		name := test.operation
		if test.transient {
			name += "_transient"
		}
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			wantErr := errors.New("request failed")
			if test.transient {
				wantErr = netretry.Transient(wantErr)
			}
			client := &fakeMutationClient{failOperation: test.operation, err: wantErr}
			executor, err := NewExecutor(&fakeDownloadSource{}, client, "/downloads", 1)
			if err != nil {
				t.Fatal(err)
			}
			executor.SetLogger(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
			err = executor.Execute(context.Background(), Plan{Downloads: []DownloadPlan{{Candidate: candidate(1, 10, 1, 1), DeleteBefore: []domain.ManagedTorrent{managedTorrent(2, 10, 1, ManagedTag)}}}})
			if !errors.Is(err, wantErr) {
				t.Fatalf("error = %v, want %v", err, wantErr)
			}
			record := logRecordWithMessage(t, decodeLogRecords(t, output.String()), "TopFree execution stopped")
			if record["phase"] != test.phase || record["confirmed_write_requests"] != float64(test.confirmed) {
				t.Errorf("execution diagnostics = %#v", record)
			}
			wantLevel := "ERROR"
			if test.transient {
				wantLevel = "WARN"
			}
			if record["level"] != wantLevel {
				t.Errorf("level = %v, want %s", record["level"], wantLevel)
			}
			if strings.Contains(output.String(), "TopFree execution plan completed") || strings.Contains(output.String(), "passkey") {
				t.Errorf("false success or private URL in logs: %s", output.String())
			}
			if len(client.calls) != test.confirmed+1 {
				t.Errorf("writes continued after failure: %#v", client.calls)
			}
		})
	}
}

func decodeLogRecords(t *testing.T, output string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log record: %v\n%s", err, line)
		}
		records = append(records, record)
	}
	return records
}

func logRecordWithMessage(t *testing.T, records []map[string]any, message string) map[string]any {
	t.Helper()
	for _, record := range records {
		if record["msg"] == message {
			return record
		}
	}
	t.Fatalf("missing log message %q: %#v", message, records)
	return nil
}
