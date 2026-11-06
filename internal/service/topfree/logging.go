package topfree

import (
	"context"
	"log/slog"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

func candidateLogAttrs(candidate domain.Candidate) []slog.Attr {
	return []slog.Attr{
		slog.String("torrent_id", string(candidate.ID)),
		slog.String("hash", string(candidate.Hash)),
		slog.String("name", candidate.Name),
		slog.Int64("size_bytes", candidate.SizeBytes),
		slog.Int("seeders", candidate.Seeders),
		slog.Int("leechers", candidate.Leechers),
		slog.Time("free_until", candidate.FreeUntil),
	}
}

func managedTorrentLogAttrs(torrent domain.ManagedTorrent) []slog.Attr {
	return []slog.Attr{
		slog.String("hash", string(torrent.Hash)),
		slog.String("name", torrent.Name),
		slog.Int64("size_bytes", torrent.SizeBytes),
		slog.Int64("completed_bytes", torrent.CompletedBytes),
		slog.Int64("amount_left_bytes", torrent.AmountLeftBytes),
		slog.String("state", torrent.State),
		slog.Time("added_at", torrent.AddedAt),
		slog.String("save_path", torrent.SavePath),
		slog.Any("tags", torrent.Tags),
	}
}

// Keep collections as separate records so every value retains a field name and identity.
func logPlanDetails(ctx context.Context, logger *slog.Logger, plan Plan, blocked bool) {
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	for _, action := range []struct {
		name   string
		hashes []domain.TorrentHash
	}{
		{"tag_as_managed", plan.TagAsManaged},
		{"unmark_for_deletion", plan.UnmarkForDeletion},
		{"mark_for_deletion", plan.MarkForDeletion},
	} {
		for _, hash := range action.hashes {
			logger.DebugContext(ctx, "TopFree tag change planned", "action", action.name, "hash", hash)
		}
	}
	for _, torrent := range plan.RemoveExpired {
		logger.LogAttrs(ctx, slog.LevelDebug, "expired TopFree download cleanup planned", managedTorrentLogAttrs(torrent)...)
	}
	for index, candidate := range plan.Queued {
		attrs := append(candidateLogAttrs(candidate), slog.Int("queue_position", index+1), slog.Bool("download_blocked", blocked))
		logger.LogAttrs(ctx, slog.LevelDebug, "TopFree queued candidate", attrs...)
	}
	for _, download := range plan.Downloads {
		attrs := append(candidateLogAttrs(download.Candidate), slog.Int("cleanup_torrents", len(download.DeleteBefore)))
		logger.LogAttrs(ctx, slog.LevelDebug, "TopFree download planned", attrs...)
		for _, torrent := range download.DeleteBefore {
			attrs := append(managedTorrentLogAttrs(torrent), slog.String("for_torrent_id", string(download.Candidate.ID)))
			logger.LogAttrs(ctx, slog.LevelDebug, "TopFree capacity cleanup planned", attrs...)
		}
	}
	for _, skipped := range plan.Skipped {
		attrs := append(candidateLogAttrs(skipped.Candidate), slog.String("reason", string(skipped.Reason)))
		if skipped.Reason == SkipInsufficientSpace {
			attrs = append(attrs, slog.Int64("available_bytes", skipped.AvailableBytes))
		}
		logger.LogAttrs(ctx, slog.LevelDebug, "TopFree candidate skipped", attrs...)
	}
}
