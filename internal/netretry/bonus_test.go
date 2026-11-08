package netretry

import (
	"context"
	"errors"
	"testing"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

func TestBonusRetriesOnlyTheFailedRead(t *testing.T) {
	t.Parallel()
	source := &bonusReadFixture{}
	tracker := NewBonusTracker(source, Policy{Retries: 1})
	account, err := tracker.BonusAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.ListSeeding(context.Background(), account.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.TorrentMetadata(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	if source.accountCalls != 1 || source.listCalls != 1 || source.metadataCalls != 2 {
		t.Fatalf("calls = %+v", source)
	}
}

type bonusReadFixture struct {
	accountCalls  int
	listCalls     int
	metadataCalls int
}

func (s *bonusReadFixture) BonusAccount(context.Context) (domain.BonusAccount, error) {
	s.accountCalls++
	return domain.BonusAccount{UserID: "42"}, nil
}

func (s *bonusReadFixture) ListSeeding(context.Context, string) ([]domain.SeededTorrent, error) {
	s.listCalls++
	return nil, nil
}

func (s *bonusReadFixture) TorrentMetadata(context.Context, domain.TorrentID) (domain.TorrentMetadata, error) {
	s.metadataCalls++
	if s.metadataCalls == 1 {
		return domain.TorrentMetadata{}, Transient(errors.New("temporary read failure"))
	}
	return domain.TorrentMetadata{}, nil
}
