package byr

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"golang.org/x/net/html"
)

var (
	freeUntilPattern   = regexp.MustCompile(`\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}`)
	sizePattern        = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*(B|KiB|MiB|GiB|TiB)\s*$`)
	torrentHashPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{40}\b`)
	passkeyPattern     = regexp.MustCompile(`\b[0-9A-Za-z]{16,64}\b`)
)

var stickyClasses = map[string]struct{}{
	"sticky-one":   {},
	"sticky-two":   {},
	"sticky-three": {},
	"sticky-buy":   {},
}

var freeClasses = map[string]struct{}{
	"pro_free":    {},
	"pro_free2up": {},
}

var byrLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func parseTopFree(reader io.Reader) ([]domain.Candidate, error) {
	document, err := html.Parse(reader)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTorrentPage, err)
	}
	table := findElement(document, "table", func(node *html.Node) bool {
		return hasClass(node, "torrents")
	})
	if table == nil {
		return nil, ErrInvalidTorrentPage
	}

	var candidates []domain.Candidate
	walkElements(table, "tr", func(row *html.Node) {
		if err != nil || len(directChildren(row, "td")) < 8 || !isTopFreeRow(row) {
			return
		}
		candidate, parseErr := parseCandidate(row)
		if parseErr != nil {
			err = parseErr
			return
		}
		candidates = append(candidates, candidate)
	})
	if err != nil {
		return nil, err
	}

	return candidates, nil
}

func parseCandidate(row *html.Node) (domain.Candidate, error) {
	cells := directChildren(row, "td")
	if len(cells) < 8 {
		return domain.Candidate{}, fmt.Errorf("%w: expected at least 8 columns", ErrInvalidTorrentPage)
	}

	detailsLink := findElement(row, "a", func(node *html.Node) bool {
		href, ok := attribute(node, "href")
		return ok && strings.Contains(href, "details.php")
	})
	if detailsLink == nil {
		return domain.Candidate{}, fmt.Errorf("%w: torrent details link missing", ErrInvalidTorrentPage)
	}
	torrentID, err := torrentIDFromLink(detailsLink)
	if err != nil {
		return domain.Candidate{}, err
	}

	name, _ := attribute(detailsLink, "title")
	name = strings.TrimSpace(name)
	if name == "" {
		name = normalizedText(detailsLink)
	}
	if name == "" {
		return domain.Candidate{}, fmt.Errorf("%w: torrent %s name missing", ErrInvalidTorrentPage, torrentID)
	}

	sticky := findElement(row, "div", func(node *html.Node) bool {
		return hasAnyClass(node, stickyClasses)
	})
	stickyTitle, _ := attribute(sticky, "title")
	freeUntilText := freeUntilPattern.FindString(stickyTitle)
	freeUntil, err := time.ParseInLocation("2006-01-02 15:04:05", freeUntilText, byrLocation)
	if err != nil {
		return domain.Candidate{}, fmt.Errorf("%w: torrent %s free deadline invalid", ErrInvalidTorrentPage, torrentID)
	}

	sizeBytes, err := parseSizeBytes(normalizedText(cells[5]))
	if err != nil {
		return domain.Candidate{}, fmt.Errorf("%w: torrent %s: %v", ErrInvalidTorrentPage, torrentID, err)
	}
	seeders, err := parseCount(normalizedText(cells[6]))
	if err != nil {
		return domain.Candidate{}, fmt.Errorf("%w: torrent %s seeders invalid", ErrInvalidTorrentPage, torrentID)
	}
	leechers, err := parseCount(normalizedText(cells[7]))
	if err != nil {
		return domain.Candidate{}, fmt.Errorf("%w: torrent %s leechers invalid", ErrInvalidTorrentPage, torrentID)
	}

	return domain.Candidate{
		ID:        torrentID,
		Name:      name,
		SizeBytes: sizeBytes,
		Seeders:   seeders,
		Leechers:  leechers,
		FreeUntil: freeUntil,
	}, nil
}

func parseTorrentHash(reader io.Reader) (domain.TorrentHash, error) {
	document, err := html.Parse(reader)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrTorrentHashNotFound, err)
	}
	pageText := normalizedText(document)
	labelIndex := strings.Index(pageText, "Hash码")
	if labelIndex < 0 {
		return "", ErrTorrentHashNotFound
	}
	searchText := pageText[labelIndex:]
	if len(searchText) > 512 {
		searchText = searchText[:512]
	}
	hash := torrentHashPattern.FindString(searchText)
	if hash == "" {
		return "", ErrTorrentHashNotFound
	}

	return domain.TorrentHash(strings.ToLower(hash)), nil
}

func parsePasskey(reader io.Reader) (string, error) {
	document, err := html.Parse(reader)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrPasskeyNotFound, err)
	}
	label := findElement(document, "td", func(node *html.Node) bool {
		return strings.EqualFold(normalizedText(node), "passkey")
	})
	if label == nil {
		return "", ErrPasskeyNotFound
	}
	valueCell := nextElementSibling(label, "td")
	if valueCell == nil {
		return "", ErrPasskeyNotFound
	}
	passkey := passkeyPattern.FindString(normalizedText(valueCell))
	if passkey == "" {
		return "", ErrPasskeyNotFound
	}
	return passkey, nil
}

func isTopFreeRow(row *html.Node) bool {
	sticky := findElement(row, "div", func(node *html.Node) bool {
		return hasAnyClass(node, stickyClasses)
	})
	if sticky == nil {
		return false
	}
	return findElement(row, "img", func(node *html.Node) bool {
		return hasAnyClass(node, freeClasses)
	}) != nil
}

func torrentIDFromLink(link *html.Node) (domain.TorrentID, error) {
	href, _ := attribute(link, "href")
	parsedURL, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("%w: torrent details URL invalid", ErrInvalidTorrentPage)
	}
	torrentID := strings.TrimSpace(parsedURL.Query().Get("id"))
	if torrentID == "" {
		return "", fmt.Errorf("%w: torrent ID missing", ErrInvalidTorrentPage)
	}
	if _, err := strconv.ParseUint(torrentID, 10, 64); err != nil {
		return "", fmt.Errorf("%w: torrent ID invalid", ErrInvalidTorrentPage)
	}

	return domain.TorrentID(torrentID), nil
}

func parseSizeBytes(text string) (int64, error) {
	match := sizePattern.FindStringSubmatch(text)
	if match == nil {
		return 0, errors.New("size format invalid")
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil || value < 0 {
		return 0, errors.New("size value invalid")
	}
	multipliers := map[string]float64{
		"b":   1,
		"kib": 1 << 10,
		"mib": 1 << 20,
		"gib": 1 << 30,
		"tib": 1 << 40,
	}
	bytes := value * multipliers[strings.ToLower(match[2])]
	if bytes > math.MaxInt64 {
		return 0, errors.New("size overflows int64")
	}

	return int64(bytes), nil
}

func parseCount(text string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || value < 0 {
		return 0, errors.New("count invalid")
	}
	return value, nil
}

func findElement(root *html.Node, tag string, match func(*html.Node) bool) *html.Node {
	var found *html.Node
	walkElements(root, tag, func(node *html.Node) {
		if found == nil && match(node) {
			found = node
		}
	})
	return found
}

func walkElements(root *html.Node, tag string, visit func(*html.Node)) {
	for node := root; node != nil; node = nextNode(root, node) {
		if node.Type == html.ElementNode && node.Data == tag {
			visit(node)
		}
	}
}

func nextNode(root, node *html.Node) *html.Node {
	if node.FirstChild != nil {
		return node.FirstChild
	}
	for node != root && node.NextSibling == nil {
		node = node.Parent
	}
	if node == root {
		return nil
	}
	return node.NextSibling
}

func directChildren(node *html.Node, tag string) []*html.Node {
	var children []*html.Node
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == tag {
			children = append(children, child)
		}
	}
	return children
}

func nextElementSibling(node *html.Node, tag string) *html.Node {
	for sibling := node.NextSibling; sibling != nil; sibling = sibling.NextSibling {
		if sibling.Type == html.ElementNode && sibling.Data == tag {
			return sibling
		}
	}
	return nil
}

func normalizedText(node *html.Node) string {
	var builder strings.Builder
	for current := node; current != nil; current = nextNode(node, current) {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func hasClass(node *html.Node, className string) bool {
	classes, ok := attribute(node, "class")
	if !ok {
		return false
	}
	for _, class := range strings.Fields(classes) {
		if class == className {
			return true
		}
	}
	return false
}

func hasAnyClass(node *html.Node, classes map[string]struct{}) bool {
	classNames, ok := attribute(node, "class")
	if !ok {
		return false
	}
	for _, className := range strings.Fields(classNames) {
		if _, ok := classes[className]; ok {
			return true
		}
	}
	return false
}

func attribute(node *html.Node, key string) (string, bool) {
	if node == nil {
		return "", false
	}
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return attribute.Val, true
		}
	}
	return "", false
}
