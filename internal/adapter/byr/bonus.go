package byr

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
	"golang.org/x/net/html"
)

var (
	bonusAPattern    = regexp.MustCompile(`A\s*=\s*([0-9]+(?:\.[0-9]+)?)\s*\)`)
	seedCountPattern = regexp.MustCompile(`([0-9]+)\s*条记录`)
	publishedPattern = regexp.MustCompile(`发布于\s*([0-9]{4}-[0-9]{2}-[0-9]{2}\s+[0-9]{2}:[0-9]{2}:[0-9]{2})`)
)

var _ port.BonusTracker = (*Client)(nil)

func (c *Client) BonusAccount(ctx context.Context) (domain.BonusAccount, error) {
	body, err := c.readBonusPage(ctx, "mybonus.php", url.Values{"show": {"seed"}})
	if err != nil {
		return domain.BonusAccount{}, err
	}
	return parseBonusAccount(bytes.NewReader(body))
}

func (c *Client) ListSeeding(ctx context.Context, userID string) ([]domain.SeededTorrent, error) {
	if id, err := strconv.ParseUint(userID, 10, 64); err != nil || id == 0 {
		return nil, fmt.Errorf("%w: invalid bonus user ID", port.ErrInvalidTrackerSnapshot)
	}
	body, err := c.readBonusPage(ctx, "getusertorrentlistajax.php", url.Values{"userid": {userID}, "type": {"seeding"}})
	if err != nil {
		return nil, err
	}
	return parseSeeding(bytes.NewReader(body))
}

// TorrentMetadata caches only immutable fields; seeder counts are always refreshed.
func (c *Client) TorrentMetadata(ctx context.Context, id domain.TorrentID) (domain.TorrentMetadata, error) {
	if err := ctx.Err(); err != nil {
		return domain.TorrentMetadata{}, err
	}
	if !validTorrentID(id) {
		return domain.TorrentMetadata{}, ErrInvalidTorrentID
	}
	c.hashMu.RLock()
	metadata, found := c.metadataCache[id]
	c.hashMu.RUnlock()
	if found {
		return metadata, nil
	}
	body, err := c.readBonusPage(ctx, "details.php", url.Values{"id": {string(id)}})
	if err != nil {
		return domain.TorrentMetadata{}, err
	}
	metadata, err = parseBonusMetadata(body)
	if err != nil {
		return domain.TorrentMetadata{}, err
	}
	c.hashMu.Lock()
	if c.metadataCache == nil {
		c.metadataCache = make(map[domain.TorrentID]domain.TorrentMetadata)
	}
	c.metadataCache[id] = metadata
	c.hashCache[id] = metadata.Hash
	c.hashMu.Unlock()
	return metadata, nil
}

func (c *Client) readBonusPage(ctx context.Context, path string, query url.Values) ([]byte, error) {
	response, err := c.get(ctx, path, query)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPageSize+1))
	if err != nil {
		return nil, fmt.Errorf("read BYR bonus page: %w", err)
	}
	if len(body) > maxPageSize {
		return nil, fmt.Errorf("%w: bonus page exceeds size limit", port.ErrInvalidTrackerSnapshot)
	}
	return body, nil
}

func parseBonusAccount(reader io.Reader) (domain.BonusAccount, error) {
	document, err := html.Parse(reader)
	if err != nil {
		return domain.BonusAccount{}, fmt.Errorf("parse BYR bonus page: %w", err)
	}
	text := normalizedText(document)
	ruleText := strings.Join(strings.Fields(text), "")
	// Refuse scoring if BYR changes the published rule or constants.
	for _, rule := range []string{
		`T0=4(?:[^0-9.]|$)`, `N0=7(?:[^0-9.]|$)`, `B0=100(?:[^0-9.]|$)`, `L=300(?:[^0-9.]|$)`,
		`(?:^|[^0-9.])1个魔力值\*你的做种数`, `做种数最多计7个`,
		`自种子发布起到现在所经过的时间`, `单位是周`,
	} {
		if !regexp.MustCompile(rule).MatchString(ruleText) {
			return domain.BonusAccount{}, fmt.Errorf("%w: unsupported BYR bonus rules", port.ErrInvalidTrackerSnapshot)
		}
	}
	expected := map[string]bool{
		"A=sigma((1-10^(-Ti/T0))*Si*(1+sqrt(2)*10^(-(Ni-1)/(N0-1": false,
		"B=B0*2/pi*arctan(A/L": false,
	}
	walkElements(document, "img", func(node *html.Node) {
		alt, _ := attribute(node, "alt")
		formula := strings.TrimRight(strings.Join(strings.Fields(alt), ""), ")")
		if _, exists := expected[formula]; exists {
			expected[formula] = true
		}
	})
	for _, found := range expected {
		if !found {
			return domain.BonusAccount{}, fmt.Errorf("%w: unsupported BYR bonus formula", port.ErrInvalidTrackerSnapshot)
		}
	}
	status := findElement(document, "td", func(node *html.Node) bool {
		return strings.Contains(normalizedText(node), "你当前每小时能获取")
	})
	if status == nil {
		return domain.BonusAccount{}, fmt.Errorf("%w: bonus account summary missing", port.ErrInvalidTrackerSnapshot)
	}
	match := bonusAPattern.FindStringSubmatch(normalizedText(status))
	if match == nil {
		return domain.BonusAccount{}, fmt.Errorf("%w: bonus account A missing", port.ErrInvalidTrackerSnapshot)
	}
	a, err := strconv.ParseFloat(match[1], 64)
	if err != nil || math.IsNaN(a) || math.IsInf(a, 0) || a < 0 {
		return domain.BonusAccount{}, fmt.Errorf("%w: bonus account A invalid", port.ErrInvalidTrackerSnapshot)
	}
	profile := findElement(document, "a", func(node *html.Node) bool {
		href, _ := attribute(node, "href")
		u, parseErr := url.Parse(href)
		return parseErr == nil && strings.TrimPrefix(u.Path, "/") == "userdetails.php" && u.Query().Get("id") != ""
	})
	if profile == nil {
		return domain.BonusAccount{}, fmt.Errorf("%w: bonus account identity missing", port.ErrInvalidTrackerSnapshot)
	}
	href, _ := attribute(profile, "href")
	u, _ := url.Parse(href)
	userID := u.Query().Get("id")
	if id, err := strconv.ParseUint(userID, 10, 64); err != nil || id == 0 {
		return domain.BonusAccount{}, fmt.Errorf("%w: bonus account identity invalid", port.ErrInvalidTrackerSnapshot)
	}
	return domain.BonusAccount{UserID: userID, A: a}, nil
}

func parseSeeding(reader io.Reader) ([]domain.SeededTorrent, error) {
	document, err := html.Parse(reader)
	if err != nil {
		return nil, fmt.Errorf("parse BYR seeding list: %w", err)
	}
	match := seedCountPattern.FindStringSubmatch(normalizedText(document))
	if match == nil {
		return nil, fmt.Errorf("%w: seeding record count missing", port.ErrInvalidTrackerSnapshot)
	}
	count, err := strconv.Atoi(match[1])
	if err != nil {
		return nil, fmt.Errorf("%w: seeding record count invalid", port.ErrInvalidTrackerSnapshot)
	}
	table := findElement(document, "table", func(node *html.Node) bool { return hasClass(node, "coltable") })
	if table == nil {
		if count == 0 {
			return []domain.SeededTorrent{}, nil
		}
		return nil, fmt.Errorf("%w: seeding table missing", port.ErrInvalidTrackerSnapshot)
	}
	var torrents []domain.SeededTorrent
	seen := make(map[domain.TorrentID]bool)
	walkElements(table, "tr", func(row *html.Node) {
		if err != nil {
			return
		}
		cells := directChildren(row, "td")
		link := findElement(row, "a", func(node *html.Node) bool {
			href, _ := attribute(node, "href")
			u, parseErr := url.Parse(href)
			return parseErr == nil && strings.TrimPrefix(u.Path, "/") == "details.php"
		})
		if link == nil {
			return
		}
		if len(cells) != 8 {
			err = fmt.Errorf("%w: seeding columns invalid", port.ErrInvalidTrackerSnapshot)
			return
		}
		var torrent domain.SeededTorrent
		torrent.ID, err = torrentIDFromLink(link)
		if err != nil {
			return
		}
		torrent.SizeBytes, err = parseSizeBytes(normalizedText(cells[2]))
		if err != nil {
			return
		}
		torrent.Seeders, err = parseCount(normalizedText(cells[3]))
		if err != nil || torrent.Seeders < 1 || torrent.SizeBytes <= 0 || seen[torrent.ID] {
			err = fmt.Errorf("%w: duplicate or invalid seeding record", port.ErrInvalidTrackerSnapshot)
			return
		}
		seen[torrent.ID] = true
		torrents = append(torrents, torrent)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: parse seeding record: %w", port.ErrInvalidTrackerSnapshot, err)
	}
	if len(torrents) != count {
		return nil, fmt.Errorf("%w: incomplete seeding list", port.ErrInvalidTrackerSnapshot)
	}
	return torrents, nil
}

func parseBonusMetadata(body []byte) (domain.TorrentMetadata, error) {
	hash, err := parseTorrentHash(bytes.NewReader(body))
	if err != nil {
		return domain.TorrentMetadata{}, fmt.Errorf("%w: %w", port.ErrInvalidTrackerSnapshot, err)
	}
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return domain.TorrentMetadata{}, fmt.Errorf("parse BYR publication metadata: %w", err)
	}
	match := publishedPattern.FindStringSubmatch(normalizedText(document))
	if match == nil {
		return domain.TorrentMetadata{}, fmt.Errorf("%w: publication time missing", port.ErrInvalidTrackerSnapshot)
	}
	published, err := time.ParseInLocation("2006-01-02 15:04:05", match[1], byrLocation)
	if err != nil {
		return domain.TorrentMetadata{}, fmt.Errorf("%w: publication time invalid", port.ErrInvalidTrackerSnapshot)
	}
	return domain.TorrentMetadata{Hash: hash, PublishedAt: published}, nil
}
