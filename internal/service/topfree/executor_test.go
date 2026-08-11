package topfree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

func TestExecutorAppliesPlanInOrder(t *testing.T) {
	t.Parallel()

	source := &fakeDownloadSource{}
	client := &fakeMutationClient{}
	executor, err := NewExecutor(source, client, " /downloads ", 1)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	var logs bytes.Buffer
	executor.SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	plan := Plan{
		TagAsManaged:      []domain.TorrentHash{torrentHash(1)},
		UnmarkForDeletion: []domain.TorrentHash{torrentHash(1)},
		MarkForDeletion:   []domain.TorrentHash{torrentHash(2)},
		Downloads: []DownloadPlan{
			{
				Candidate:    candidate(3, 40, 1, 1),
				DeleteBefore: []domain.ManagedTorrent{managedTorrent(4, 20, 1, ManagedTag), managedTorrent(5, 30, 2, ManagedTag)},
			},
			{Candidate: candidate(6, 10, 1, 1)},
		},
	}

	if err := executor.Execute(context.Background(), plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !reflect.DeepEqual(source.calls, []domain.TorrentID{"3", "6"}) {
		t.Errorf("source calls = %v", source.calls)
	}
	wantCalls := []mutationCall{
		{name: "addTags", hashes: hashes(1), tags: []string{ManagedTag}},
		{name: "removeTags", hashes: hashes(1), tags: []string{DeleteTag}},
		{name: "addTags", hashes: hashes(2), tags: []string{DeleteTag}},
		{name: "reannounce", hashes: hashes(4, 5)},
		{name: "pause", hashes: hashes(4, 5)},
		{name: "delete", hashes: hashes(4, 5), deleteFiles: true},
		{name: "add", addRequest: port.AddTorrentRequest{SourceURL: fixtureDownloadURL("3"), SavePath: "/downloads", Tags: []string{ManagedTag}}},
		{name: "add", addRequest: port.AddTorrentRequest{SourceURL: fixtureDownloadURL("6"), SavePath: "/downloads", Tags: []string{ManagedTag}}},
	}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Errorf("client calls differ:\n got: %#v\nwant: %#v", client.calls, wantCalls)
	}
	if client.freeSpaceCalls != 2 {
		t.Errorf("FreeSpace() calls = %d, want 2", client.freeSpaceCalls)
	}
	for _, expected := range []string{"preflighting TopFree download", "reannouncing cleanup torrents", "stopping cleanup torrents", "deleting cleanup torrents", "adding TopFree torrent"} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("execution logs do not contain %q:\n%s", expected, logs.String())
		}
	}
	if strings.Contains(logs.String(), "passkey") || strings.Contains(logs.String(), fixtureDownloadURL("3")) {
		t.Errorf("execution logs contain a sensitive download URL: %s", logs.String())
	}
}

func TestExecutorPreflightsAllDownloadsBeforeWrites(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("tracker unavailable")
	source := &fakeDownloadSource{failID: "6", err: wantErr}
	client := &fakeMutationClient{}
	executor, err := NewExecutor(source, client, "", 1)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	plan := Plan{
		TagAsManaged: []domain.TorrentHash{torrentHash(1)},
		Downloads: []DownloadPlan{
			{Candidate: candidate(3, 10, 1, 1)},
			{Candidate: candidate(6, 10, 1, 1)},
		},
	}
	err = executor.Execute(context.Background(), plan)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want tracker error", err)
	}
	if !reflect.DeepEqual(source.calls, []domain.TorrentID{"3", "6"}) {
		t.Errorf("source calls = %v", source.calls)
	}
	if len(client.calls) != 0 {
		t.Errorf("client received writes after failed preflight: %#v", client.calls)
	}
}

func TestExecutorRejectsInvalidDownloadURLBeforeWrites(t *testing.T) {
	t.Parallel()

	source := &fakeDownloadSource{urlByID: map[domain.TorrentID]string{"3": "file:///tmp/torrent"}}
	client := &fakeMutationClient{}
	executor, err := NewExecutor(source, client, "", 1)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	err = executor.Execute(context.Background(), Plan{
		MarkForDeletion: []domain.TorrentHash{torrentHash(2)},
		Downloads:       []DownloadPlan{{Candidate: candidate(3, 10, 1, 1)}},
	})
	if !errors.Is(err, ErrInvalidDownloadURL) {
		t.Fatalf("Execute() error = %v, want ErrInvalidDownloadURL", err)
	}
	if len(client.calls) != 0 {
		t.Errorf("client received writes after invalid URL: %#v", client.calls)
	}
}

func TestExecutorStopsOnFirstMutationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("stop failed")
	source := &fakeDownloadSource{}
	client := &fakeMutationClient{failOperation: "pause", err: wantErr}
	executor, err := NewExecutor(source, client, "", 1)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	err = executor.Execute(context.Background(), Plan{Downloads: []DownloadPlan{{
		Candidate:    candidate(3, 10, 1, 1),
		DeleteBefore: []domain.ManagedTorrent{managedTorrent(4, 20, 1, ManagedTag)},
	}}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want mutation error", err)
	}
	wantCalls := []mutationCall{
		{name: "reannounce", hashes: hashes(4)},
		{name: "pause", hashes: hashes(4)},
	}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Errorf("client calls = %#v, want %#v", client.calls, wantCalls)
	}
}

func TestExecutorRejectsPredictableDiskShortageBeforeWrites(t *testing.T) {
	t.Parallel()

	client := &fakeMutationClient{freeSpaceSequence: []int64{30}}
	executor, err := NewExecutor(&fakeDownloadSource{}, client, "/downloads", 10)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	cleanup := managedTorrent(4, 10, 1, ManagedTag)
	cleanup.SavePath = "/downloads"
	err = executor.Execute(context.Background(), Plan{Downloads: []DownloadPlan{{
		Candidate:    candidate(3, 40, 1, 1),
		DeleteBefore: []domain.ManagedTorrent{cleanup},
	}}})
	if !errors.Is(err, ErrInsufficientDiskSpace) {
		t.Fatalf("Execute() error = %v, want ErrInsufficientDiskSpace", err)
	}
	if len(client.calls) != 0 {
		t.Errorf("client received writes after predictable shortage: %#v", client.calls)
	}
}

func TestExecutorRechecksDiskSpaceAfterCleanupBeforeAdd(t *testing.T) {
	t.Parallel()

	client := &fakeMutationClient{freeSpaceSequence: []int64{30, 49}}
	executor, err := NewExecutor(&fakeDownloadSource{}, client, "/downloads", 10)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	cleanup := managedTorrent(4, 30, 1, ManagedTag)
	cleanup.SavePath = "/downloads"
	err = executor.Execute(context.Background(), Plan{Downloads: []DownloadPlan{{
		Candidate:    candidate(3, 40, 1, 1),
		DeleteBefore: []domain.ManagedTorrent{cleanup},
	}}})
	if !errors.Is(err, ErrInsufficientDiskSpace) {
		t.Fatalf("Execute() error = %v, want ErrInsufficientDiskSpace", err)
	}
	wantOperations := []string{"reannounce", "pause", "delete"}
	if len(client.calls) != len(wantOperations) {
		t.Fatalf("client calls = %#v", client.calls)
	}
	for index, operation := range wantOperations {
		if client.calls[index].name != operation {
			t.Errorf("calls[%d] = %s, want %s", index, client.calls[index].name, operation)
		}
	}
	if client.freeSpaceCalls != 2 {
		t.Errorf("FreeSpace() calls = %d, want 2", client.freeSpaceCalls)
	}
}

func TestExecutorUsesDiskSafeWriteOrder(t *testing.T) {
	t.Parallel()

	client := &fakeMutationClient{freeSpaceSequence: []int64{30, 60}}
	executor, err := NewExecutor(&fakeDownloadSource{}, client, "/downloads", 10)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	cleanup := managedTorrent(4, 30, 1, ManagedTag)
	cleanup.SavePath = "/downloads"
	err = executor.Execute(context.Background(), Plan{Downloads: []DownloadPlan{{
		Candidate:    candidate(3, 40, 1, 1),
		DeleteBefore: []domain.ManagedTorrent{cleanup},
	}}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	wantEvents := []string{"freeSpace", "reannounce", "pause", "delete", "freeSpace", "add"}
	if !reflect.DeepEqual(client.events, wantEvents) {
		t.Errorf("events = %v, want %v", client.events, wantEvents)
	}
}

func TestExecutorRejectsSavePathChangeAfterCleanup(t *testing.T) {
	t.Parallel()

	client := &fakeMutationClient{
		freeSpaceSequence: []int64{100, 100},
		freeSpacePaths:    []string{"/downloads", "/other-disk"},
	}
	executor, err := NewExecutor(&fakeDownloadSource{}, client, "", 10)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	plan := Plan{
		EffectiveSavePath: "/downloads",
		MinFreeSpaceBytes: 10,
		Downloads:         []DownloadPlan{{Candidate: candidate(3, 40, 1, 1)}},
	}
	err = executor.Execute(context.Background(), plan)
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("Execute() error = %v, want ErrInvalidPlan", err)
	}
	if len(client.calls) != 0 {
		t.Errorf("client received writes after path change: %#v", client.calls)
	}
}

func TestExecutorRejectsUnsafePlanBeforePreflight(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan Plan
	}{
		{name: "invalid action hash", plan: Plan{TagAsManaged: []domain.TorrentHash{"all"}}},
		{name: "duplicate action hash", plan: Plan{TagAsManaged: []domain.TorrentHash{torrentHash(1), torrentHash(1)}}},
		{name: "mark and unmark conflict", plan: Plan{MarkForDeletion: hashes(1), UnmarkForDeletion: hashes(1)}},
		{name: "download and delete conflict", plan: Plan{Downloads: []DownloadPlan{
			{Candidate: candidate(1, 10, 1, 1), DeleteBefore: []domain.ManagedTorrent{managedTorrent(1, 10, 1, ManagedTag)}},
		}}},
		{name: "duplicate deletion", plan: Plan{Downloads: []DownloadPlan{
			{Candidate: candidate(1, 10, 1, 1), DeleteBefore: []domain.ManagedTorrent{managedTorrent(8, 10, 1, ManagedTag)}},
			{Candidate: candidate(2, 10, 1, 1), DeleteBefore: []domain.ManagedTorrent{managedTorrent(8, 10, 1, ManagedTag)}},
		}}},
		{name: "invalid candidate", plan: Plan{Downloads: []DownloadPlan{{Candidate: domain.Candidate{}}}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := &fakeDownloadSource{}
			client := &fakeMutationClient{}
			executor, err := NewExecutor(source, client, "", 1)
			if err != nil {
				t.Fatalf("NewExecutor() error = %v", err)
			}
			if err := executor.Execute(context.Background(), test.plan); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("Execute() error = %v, want ErrInvalidPlan", err)
			}
			if len(source.calls) != 0 || len(client.calls) != 0 {
				t.Errorf("unsafe plan reached dependencies")
			}
		})
	}
}

func TestExecutorHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	source := &fakeDownloadSource{}
	client := &fakeMutationClient{}
	executor, err := NewExecutor(source, client, "", 1)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = executor.Execute(ctx, Plan{Downloads: []DownloadPlan{{Candidate: candidate(1, 10, 1, 1)}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
	if len(source.calls) != 0 || len(client.calls) != 0 {
		t.Error("canceled execution reached dependencies")
	}
}

func TestNewExecutorValidatesDependencies(t *testing.T) {
	t.Parallel()

	if _, err := NewExecutor(nil, &fakeMutationClient{}, "", 1); !errors.Is(err, ErrDownloadSourceRequired) {
		t.Errorf("nil source error = %v", err)
	}
	if _, err := NewExecutor(&fakeDownloadSource{}, nil, "", 1); !errors.Is(err, ErrMutationClientRequired) {
		t.Errorf("nil client error = %v", err)
	}
	if _, err := NewExecutor(&fakeDownloadSource{}, &fakeMutationClient{}, "", 0); !errors.Is(err, ErrInvalidFreeSpaceReserve) {
		t.Errorf("invalid reserve error = %v", err)
	}
}

func TestPlannerExecutorLocalIntegration(t *testing.T) {
	t.Parallel()

	firstCandidate := candidate(1, 100, 1, 10)
	secondCandidate := candidate(2, 60, 1, 1)
	firstPlan, err := BuildPlan(150, []domain.Candidate{secondCandidate, firstCandidate}, nil)
	if err != nil {
		t.Fatalf("first BuildPlan() error = %v", err)
	}
	firstSource := &fakeDownloadSource{}
	firstClient := &fakeMutationClient{}
	firstExecutor, err := NewExecutor(firstSource, firstClient, "/downloads", 1)
	if err != nil {
		t.Fatalf("first NewExecutor() error = %v", err)
	}
	if err := firstExecutor.Execute(context.Background(), firstPlan); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	if len(firstClient.calls) != 1 || firstClient.calls[0].name != "add" || !strings.Contains(firstClient.calls[0].addRequest.SourceURL, "id=1") {
		t.Fatalf("unexpected first execution: %#v", firstClient.calls)
	}

	simulatedManaged := []domain.ManagedTorrent{managedTorrent(1, 100, 1, ManagedTag)}
	secondPlan, err := BuildPlan(150, []domain.Candidate{secondCandidate}, simulatedManaged)
	if err != nil {
		t.Fatalf("second BuildPlan() error = %v", err)
	}
	secondSource := &fakeDownloadSource{}
	secondClient := &fakeMutationClient{}
	secondExecutor, err := NewExecutor(secondSource, secondClient, "/downloads", 1)
	if err != nil {
		t.Fatalf("second NewExecutor() error = %v", err)
	}
	if err := secondExecutor.Execute(context.Background(), secondPlan); err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	wantOperations := []string{"reannounce", "pause", "delete", "add"}
	if len(secondClient.calls) != len(wantOperations) {
		t.Fatalf("second execution calls = %#v", secondClient.calls)
	}
	for index, operation := range wantOperations {
		if secondClient.calls[index].name != operation {
			t.Errorf("second calls[%d] = %s, want %s", index, secondClient.calls[index].name, operation)
		}
	}
}

type fakeDownloadSource struct {
	calls   []domain.TorrentID
	urlByID map[domain.TorrentID]string
	failID  domain.TorrentID
	err     error
}

func (source *fakeDownloadSource) DownloadURL(_ context.Context, torrentID domain.TorrentID) (string, error) {
	source.calls = append(source.calls, torrentID)
	if torrentID == source.failID {
		return "", source.err
	}
	if value, exists := source.urlByID[torrentID]; exists {
		return value, nil
	}
	return fixtureDownloadURL(string(torrentID)), nil
}

type mutationCall struct {
	name        string
	hashes      []domain.TorrentHash
	tags        []string
	addRequest  port.AddTorrentRequest
	deleteFiles bool
}

type fakeMutationClient struct {
	calls             []mutationCall
	events            []string
	failOperation     string
	err               error
	freeSpaceSequence []int64
	freeSpacePath     string
	freeSpacePaths    []string
	freeSpaceErr      error
	freeSpaceCalls    int
}

func (client *fakeMutationClient) FreeSpace(ctx context.Context, configuredPath string) (domain.DiskSpace, error) {
	if err := ctx.Err(); err != nil {
		return domain.DiskSpace{}, err
	}
	client.freeSpaceCalls++
	client.events = append(client.events, "freeSpace")
	if client.freeSpaceErr != nil {
		return domain.DiskSpace{}, client.freeSpaceErr
	}
	path := strings.TrimSpace(configuredPath)
	if client.freeSpacePath != "" {
		path = client.freeSpacePath
	}
	if len(client.freeSpacePaths) > 0 {
		index := client.freeSpaceCalls - 1
		if index >= len(client.freeSpacePaths) {
			index = len(client.freeSpacePaths) - 1
		}
		path = client.freeSpacePaths[index]
	}
	if path == "" {
		path = "/downloads"
	}
	freeBytes := int64(1 << 60)
	if len(client.freeSpaceSequence) > 0 {
		index := client.freeSpaceCalls - 1
		if index >= len(client.freeSpaceSequence) {
			index = len(client.freeSpaceSequence) - 1
		}
		freeBytes = client.freeSpaceSequence[index]
	}
	return domain.DiskSpace{Path: path, FreeBytes: freeBytes}, nil
}

func (client *fakeMutationClient) AddTags(_ context.Context, hashValues []domain.TorrentHash, tags []string) error {
	return client.record(mutationCall{name: "addTags", hashes: cloneHashes(hashValues), tags: append([]string(nil), tags...)})
}

func (client *fakeMutationClient) RemoveTags(_ context.Context, hashValues []domain.TorrentHash, tags []string) error {
	return client.record(mutationCall{name: "removeTags", hashes: cloneHashes(hashValues), tags: append([]string(nil), tags...)})
}

func (client *fakeMutationClient) Add(_ context.Context, request port.AddTorrentRequest) error {
	request.Tags = append([]string(nil), request.Tags...)
	return client.record(mutationCall{name: "add", addRequest: request})
}

func (client *fakeMutationClient) Reannounce(_ context.Context, hashValues []domain.TorrentHash) error {
	return client.record(mutationCall{name: "reannounce", hashes: cloneHashes(hashValues)})
}

func (client *fakeMutationClient) Pause(_ context.Context, hashValues []domain.TorrentHash) error {
	return client.record(mutationCall{name: "pause", hashes: cloneHashes(hashValues)})
}

func (client *fakeMutationClient) Delete(_ context.Context, hashValues []domain.TorrentHash, deleteFiles bool) error {
	return client.record(mutationCall{name: "delete", hashes: cloneHashes(hashValues), deleteFiles: deleteFiles})
}

func (client *fakeMutationClient) record(call mutationCall) error {
	client.calls = append(client.calls, call)
	client.events = append(client.events, call.name)
	if call.name == client.failOperation {
		return client.err
	}
	return nil
}

func fixtureDownloadURL(id string) string {
	return fmt.Sprintf("https://byr.pt/download.php?id=%s&passkey=fixture", id)
}

func hashes(numbers ...int) []domain.TorrentHash {
	values := make([]domain.TorrentHash, len(numbers))
	for index, number := range numbers {
		values[index] = torrentHash(number)
	}
	return values
}

func cloneHashes(values []domain.TorrentHash) []domain.TorrentHash {
	return append([]domain.TorrentHash(nil), values...)
}
