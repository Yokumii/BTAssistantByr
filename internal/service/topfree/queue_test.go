package topfree

import (
	"testing"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

func TestDownloadQueueReconcilesTrackerAndClientSnapshots(t *testing.T) {
	t.Parallel()

	first := candidate(1, 10, 1, 1)
	second := candidate(2, 20, 1, 1)
	var queue downloadQueue

	initial, err := queue.sync([]domain.Candidate{first, second}, nil)
	if err != nil {
		t.Fatalf("initial sync error = %v", err)
	}
	if len(initial.candidates) != 2 || initial.downloadBlocked {
		t.Fatalf("initial queue = %+v", initial)
	}

	queue.markSubmitted(first.Hash)
	pending, err := queue.sync([]domain.Candidate{first, second}, nil)
	if err != nil {
		t.Fatalf("pending sync error = %v", err)
	}
	if !pending.downloadBlocked || len(pending.candidates) != 1 || pending.candidates[0].Hash != second.Hash {
		t.Fatalf("pending queue = %+v", pending)
	}

	downloading := managedTorrent(1, 10, 1, ManagedTag)
	downloading.CompletedBytes = 1
	downloading.AmountLeftBytes = 9
	downloading.State = "metaDL"
	visible, err := queue.sync([]domain.Candidate{first, second}, []domain.ManagedTorrent{downloading})
	if err != nil {
		t.Fatalf("visible sync error = %v", err)
	}
	if !visible.downloadBlocked || len(visible.candidates) != 1 || visible.candidates[0].Hash != second.Hash {
		t.Fatalf("visible queue = %+v", visible)
	}

	downloading.CompletedBytes = downloading.SizeBytes
	downloading.AmountLeftBytes = 0
	downloading.State = "stalledUP"
	completed, err := queue.sync([]domain.Candidate{first, second}, []domain.ManagedTorrent{downloading})
	if err != nil {
		t.Fatalf("completed sync error = %v", err)
	}
	if completed.downloadBlocked || len(completed.candidates) != 1 || completed.candidates[0].Hash != second.Hash {
		t.Fatalf("completed queue = %+v", completed)
	}
}

func TestDownloadQueueDropsTorrentNoLongerTopFree(t *testing.T) {
	t.Parallel()

	first := candidate(1, 10, 1, 1)
	second := candidate(2, 20, 1, 1)
	var queue downloadQueue
	if _, err := queue.sync([]domain.Candidate{first, second}, nil); err != nil {
		t.Fatalf("initial sync error = %v", err)
	}
	snapshot, err := queue.sync([]domain.Candidate{second}, nil)
	if err != nil {
		t.Fatalf("second sync error = %v", err)
	}
	if len(snapshot.removed) != 1 || snapshot.removed[0] != first.Hash || len(snapshot.candidates) != 1 || snapshot.candidates[0].Hash != second.Hash {
		t.Fatalf("reconciled queue = %+v", snapshot)
	}
}
