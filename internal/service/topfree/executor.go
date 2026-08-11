package topfree

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

var (
	// ErrDownloadSourceRequired indicates that no BYR download URL source was supplied.
	ErrDownloadSourceRequired = errors.New("TopFree download URL source is required")
	// ErrMutationClientRequired indicates that no torrent mutation client was supplied.
	ErrMutationClientRequired = errors.New("TopFree mutation client is required")
	// ErrInvalidPlan indicates that a plan cannot be executed safely.
	ErrInvalidPlan = errors.New("invalid TopFree execution plan")
	// ErrInvalidDownloadURL indicates that BYR returned an unusable torrent URL.
	ErrInvalidDownloadURL = errors.New("invalid TopFree download URL")
)

// DownloadURLSource resolves a tracker torrent ID without downloading it locally.
type DownloadURLSource interface {
	DownloadURL(context.Context, domain.TorrentID) (string, error)
}

// MutationClient contains only the qBittorrent writes required by plan execution.
type MutationClient interface {
	AddTags(context.Context, []domain.TorrentHash, []string) error
	RemoveTags(context.Context, []domain.TorrentHash, []string) error
	Add(context.Context, port.AddTorrentRequest) error
	Reannounce(context.Context, []domain.TorrentHash) error
	Pause(context.Context, []domain.TorrentHash) error
	Delete(context.Context, []domain.TorrentHash, bool) error
}

// Executor applies a previously computed Plan. It is not started automatically.
type Executor struct {
	source   DownloadURLSource
	client   MutationClient
	savePath string
	logger   *slog.Logger
}

// SetLogger enables detailed execution logs. It must be called before Execute.
func (e *Executor) SetLogger(logger *slog.Logger) {
	e.logger = logger
}

// NewExecutor creates an explicit TopFree plan executor.
func NewExecutor(source DownloadURLSource, client MutationClient, savePath string) (*Executor, error) {
	if source == nil {
		return nil, ErrDownloadSourceRequired
	}
	if client == nil {
		return nil, ErrMutationClientRequired
	}
	return &Executor{
		source:   source,
		client:   client,
		savePath: strings.TrimSpace(savePath),
	}, nil
}

// Execute validates and preflights the complete plan before applying writes in order.
// It stops on the first error; already completed external writes are not rolled back.
func (e *Executor) Execute(ctx context.Context, plan Plan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	prepared, err := prepareExecutionPlan(plan)
	if err != nil {
		return err
	}
	if e.logger != nil {
		level := slog.LevelDebug
		if len(prepared.tagAsManaged)+len(prepared.unmarkForDeletion)+len(prepared.markForDeletion)+len(prepared.downloads) > 0 {
			level = slog.LevelInfo
		}
		e.logger.Log(ctx, level, "TopFree execution plan accepted",
			"tag_as_managed", len(prepared.tagAsManaged),
			"unmark_for_deletion", len(prepared.unmarkForDeletion),
			"mark_for_deletion", len(prepared.markForDeletion),
			"downloads", len(prepared.downloads),
		)
	}
	if err := e.preflightDownloads(ctx, prepared.downloads); err != nil {
		return err
	}

	if err := e.addTags(ctx, prepared.tagAsManaged, ManagedTag, "tag current TopFree torrents as managed"); err != nil {
		return err
	}
	if err := e.removeTags(ctx, prepared.unmarkForDeletion, DeleteTag, "unmark current TopFree torrents for deletion"); err != nil {
		return err
	}
	if err := e.addTags(ctx, prepared.markForDeletion, DeleteTag, "mark stale TopFree torrents for deletion"); err != nil {
		return err
	}

	for _, download := range prepared.downloads {
		if err := e.executeDownload(ctx, download); err != nil {
			return err
		}
	}
	if e.logger != nil {
		level := slog.LevelDebug
		if len(prepared.tagAsManaged)+len(prepared.unmarkForDeletion)+len(prepared.markForDeletion)+len(prepared.downloads) > 0 {
			level = slog.LevelInfo
		}
		e.logger.Log(ctx, level, "TopFree execution plan completed")
	}
	return nil
}

type preparedExecutionPlan struct {
	tagAsManaged      []domain.TorrentHash
	unmarkForDeletion []domain.TorrentHash
	markForDeletion   []domain.TorrentHash
	downloads         []preparedDownload
}

type preparedDownload struct {
	candidate    domain.Candidate
	deleteBefore []domain.ManagedTorrent
	sourceURL    string
}

func prepareExecutionPlan(plan Plan) (*preparedExecutionPlan, error) {
	tagAsManaged, err := normalizeExecutionHashes("TagAsManaged", plan.TagAsManaged)
	if err != nil {
		return nil, err
	}
	unmarkForDeletion, err := normalizeExecutionHashes("UnmarkForDeletion", plan.UnmarkForDeletion)
	if err != nil {
		return nil, err
	}
	markForDeletion, err := normalizeExecutionHashes("MarkForDeletion", plan.MarkForDeletion)
	if err != nil {
		return nil, err
	}

	candidates := make([]domain.Candidate, len(plan.Downloads))
	deleteCounts := make([]int, len(plan.Downloads))
	var deletions []domain.ManagedTorrent
	for index, download := range plan.Downloads {
		candidates[index] = download.Candidate
		deleteCounts[index] = len(download.DeleteBefore)
		deletions = append(deletions, download.DeleteBefore...)
	}
	normalizedCandidates, err := normalizeCandidates(candidates)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	normalizedDeletions, err := normalizeManaged(deletions)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}

	roles := make(map[domain.TorrentHash]string)
	if err := addExecutionRoles(roles, tagAsManaged, "TagAsManaged", ""); err != nil {
		return nil, err
	}
	if err := addExecutionRoles(roles, unmarkForDeletion, "UnmarkForDeletion", "TagAsManaged"); err != nil {
		return nil, err
	}
	if err := addExecutionRoles(roles, markForDeletion, "MarkForDeletion", ""); err != nil {
		return nil, err
	}
	for _, candidate := range normalizedCandidates {
		if err := addExecutionRoles(roles, []domain.TorrentHash{candidate.Hash}, "Downloads", ""); err != nil {
			return nil, err
		}
	}
	for _, torrent := range normalizedDeletions {
		if err := addExecutionRoles(roles, []domain.TorrentHash{torrent.Hash}, "DeleteBefore", ""); err != nil {
			return nil, err
		}
	}

	prepared := &preparedExecutionPlan{
		tagAsManaged:      tagAsManaged,
		unmarkForDeletion: unmarkForDeletion,
		markForDeletion:   markForDeletion,
		downloads:         make([]preparedDownload, len(normalizedCandidates)),
	}
	deletionIndex := 0
	for index, candidate := range normalizedCandidates {
		deleteCount := deleteCounts[index]
		prepared.downloads[index] = preparedDownload{
			candidate:    candidate,
			deleteBefore: append([]domain.ManagedTorrent(nil), normalizedDeletions[deletionIndex:deletionIndex+deleteCount]...),
		}
		deletionIndex += deleteCount
	}
	return prepared, nil
}

func normalizeExecutionHashes(field string, hashes []domain.TorrentHash) ([]domain.TorrentHash, error) {
	normalized := make([]domain.TorrentHash, len(hashes))
	seen := make(map[domain.TorrentHash]struct{}, len(hashes))
	for index, hash := range hashes {
		hash = canonicalHash(hash)
		if !planHashPattern.MatchString(string(hash)) {
			return nil, fmt.Errorf("%w: %s contains an invalid hash at index %d", ErrInvalidPlan, field, index)
		}
		if _, exists := seen[hash]; exists {
			return nil, fmt.Errorf("%w: %s contains duplicate hash %s", ErrInvalidPlan, field, hash)
		}
		seen[hash] = struct{}{}
		normalized[index] = hash
	}
	return normalized, nil
}

func addExecutionRoles(
	roles map[domain.TorrentHash]string,
	hashes []domain.TorrentHash,
	role string,
	allowedExistingRole string,
) error {
	for _, hash := range hashes {
		if existingRole, exists := roles[hash]; exists && existingRole != allowedExistingRole {
			return fmt.Errorf("%w: hash %s appears in both %s and %s", ErrInvalidPlan, hash, existingRole, role)
		}
		if _, exists := roles[hash]; !exists {
			roles[hash] = role
		}
	}
	return nil
}

func (e *Executor) preflightDownloads(ctx context.Context, downloads []preparedDownload) error {
	for index := range downloads {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.logger != nil {
			e.logger.DebugContext(ctx, "preflighting TopFree download", "torrent_id", downloads[index].candidate.ID, "name", downloads[index].candidate.Name)
		}
		sourceURL, err := e.source.DownloadURL(ctx, downloads[index].candidate.ID)
		if err != nil {
			return fmt.Errorf("resolve download URL for torrent %s: %w", downloads[index].candidate.ID, err)
		}
		if !validDownloadURL(sourceURL) {
			return fmt.Errorf("%w for torrent %s", ErrInvalidDownloadURL, downloads[index].candidate.ID)
		}
		downloads[index].sourceURL = strings.TrimSpace(sourceURL)
		if e.logger != nil {
			e.logger.DebugContext(ctx, "TopFree download preflight completed", "torrent_id", downloads[index].candidate.ID)
		}
	}
	return nil
}

func (e *Executor) addTags(ctx context.Context, hashes []domain.TorrentHash, tag, operation string) error {
	if len(hashes) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.logger != nil {
		e.logger.DebugContext(ctx, operation, "hashes", hashes, "tag", tag)
	}
	if err := e.client.AddTags(ctx, hashes, []string{tag}); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func (e *Executor) removeTags(ctx context.Context, hashes []domain.TorrentHash, tag, operation string) error {
	if len(hashes) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.logger != nil {
		e.logger.DebugContext(ctx, operation, "hashes", hashes, "tag", tag)
	}
	if err := e.client.RemoveTags(ctx, hashes, []string{tag}); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func (e *Executor) executeDownload(ctx context.Context, download preparedDownload) error {
	hashes := make([]domain.TorrentHash, len(download.deleteBefore))
	for index, torrent := range download.deleteBefore {
		hashes[index] = torrent.Hash
	}
	if len(hashes) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.logger != nil {
			e.logger.DebugContext(ctx, "reannouncing cleanup torrents", "hashes", hashes, "before_torrent_id", download.candidate.ID)
		}
		if err := e.client.Reannounce(ctx, hashes); err != nil {
			return fmt.Errorf("reannounce cleanup torrents before adding %s: %w", download.candidate.ID, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.logger != nil {
			e.logger.DebugContext(ctx, "stopping cleanup torrents", "hashes", hashes, "before_torrent_id", download.candidate.ID)
		}
		if err := e.client.Pause(ctx, hashes); err != nil {
			return fmt.Errorf("stop cleanup torrents before adding %s: %w", download.candidate.ID, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.logger != nil {
			e.logger.DebugContext(ctx, "deleting cleanup torrents", "hashes", hashes, "delete_files", true, "before_torrent_id", download.candidate.ID)
		}
		if err := e.client.Delete(ctx, hashes, true); err != nil {
			return fmt.Errorf("delete cleanup torrents before adding %s: %w", download.candidate.ID, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.logger != nil {
		e.logger.DebugContext(ctx, "adding TopFree torrent", "torrent_id", download.candidate.ID, "name", download.candidate.Name, "save_path", e.savePath, "tag", ManagedTag)
	}
	if err := e.client.Add(ctx, port.AddTorrentRequest{
		SourceURL: download.sourceURL,
		SavePath:  e.savePath,
		Tags:      []string{ManagedTag},
	}); err != nil {
		return fmt.Errorf("add TopFree torrent %s: %w", download.candidate.ID, err)
	}
	return nil
}

func validDownloadURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}
