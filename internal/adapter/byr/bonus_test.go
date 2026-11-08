package byr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/netretry"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

const bonusPageFixture = `<html><body>
<a href="userdetails.php?id=42">owner</a>
<table><tr><td>
1个魔力值 * 你的做种数 (做种数最多计7个)
<img alt="A = sigma( ( 1 - 10 ^ ( - Ti / T0 ) ) * Si * ( 1 + sqrt( 2 ) * 10 ^ ( - ( Ni - 1 ) / ( N0 - 1 ) ) )">
<img alt="B = B0 * 2 / pi * arctan( A / L )">
T<sub>0</sub> = 4 ; N<sub>0</sub> = 7 ; B<sub>0</sub> = 100 ; L = 300
Ti 为自种子发布起到现在所经过的时间，单位是周
你当前每小时能获取97.924个魔力值 (A = 2090.1)
</td></tr></table></body></html>`

const bonusSeedingFixture = `<b>1</b> 条记录&nbsp;总量:1.00 GiB<br>
<table class="coltable block-left wide"><tr><td>类型</td><td>标题</td><td>大小</td><td>种子数</td><td>下载数</td><td>上传</td><td>下载</td><td>分享率</td></tr>
<tr><td>动漫</td><td><a href="details.php?id=123&amp;hit=1">title</a></td><td>1.00<br>GiB</td><td>3</td><td>0</td><td>1 GiB</td><td>1 GiB</td><td>1</td></tr></table>`

const bonusMetadataFixture = `<html><body><p>由 uploader 发布于2026-01-02 03:04:05</p>
<p>Hash码: 0123456789abcdef0123456789abcdef01234567</p></body></html>`

func TestParseBonusAccountAndDetectRuleChanges(t *testing.T) {
	t.Parallel()
	account, err := parseBonusAccount(strings.NewReader(bonusPageFixture))
	if err != nil || account.UserID != "42" || account.A != 2090.1 {
		t.Fatalf("account = %+v, error = %v", account, err)
	}
	for _, page := range []string{
		strings.Replace(bonusPageFixture, "= 100", "= 200", 1),
		strings.Replace(bonusPageFixture, "做种数最多计7个", "做种数最多计8个", 1),
		strings.Replace(bonusPageFixture, "1个魔力值", "2个魔力值", 1),
		strings.Replace(bonusPageFixture, "自种子发布起到现在所经过的时间", "个人累计做种时间", 1),
		strings.Replace(bonusPageFixture, "sqrt( 2 )", "sqrt( 3 )", 1),
		strings.Replace(bonusPageFixture, "A = 2090.1", "A = unknown", 1),
		"<html>maintenance</html>",
	} {
		_, err := parseBonusAccount(strings.NewReader(page))
		if !errors.Is(err, port.ErrInvalidTrackerSnapshot) || netretry.IsTransient(err) {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestParseSeedingRequiresCompleteDistinctRecords(t *testing.T) {
	t.Parallel()
	torrents, err := parseSeeding(strings.NewReader(bonusSeedingFixture))
	if err != nil || len(torrents) != 1 || torrents[0].ID != "123" || torrents[0].SizeBytes != 1<<30 || torrents[0].Seeders != 3 {
		t.Fatalf("seeding = %+v, error = %v", torrents, err)
	}
	for _, page := range []string{
		strings.Replace(bonusSeedingFixture, "<b>1</b>", "<b>2</b>", 1),
		strings.Replace(bonusSeedingFixture, "<td>3</td>", "<td>0</td>", 1),
		strings.Replace(bonusSeedingFixture, "1.00<br>GiB", "unknown", 1),
		"<html>login required</html>",
	} {
		_, err := parseSeeding(strings.NewReader(page))
		if !errors.Is(err, port.ErrInvalidTrackerSnapshot) {
			t.Fatalf("error = %v", err)
		}
	}
	if torrents, err := parseSeeding(strings.NewReader("<b>0</b> 条记录")); err != nil || len(torrents) != 0 {
		t.Fatalf("explicit empty list = %v, error = %v", torrents, err)
	}
}

func TestBonusMetadataUsesPublicationTime(t *testing.T) {
	t.Parallel()
	metadata, err := parseBonusMetadata([]byte(bonusMetadataFixture))
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, byrLocation)
	if err != nil || metadata.Hash != "0123456789abcdef0123456789abcdef01234567" || !metadata.PublishedAt.Equal(want) {
		t.Fatalf("metadata = %+v, error = %v", metadata, err)
	}
	_, err = parseBonusMetadata([]byte(strings.Replace(bonusMetadataFixture, "发布于2026-01-02 03:04:05", "最近活动2026-01-02 03:04:05", 1)))
	if !errors.Is(err, port.ErrInvalidTrackerSnapshot) {
		t.Fatalf("activity date must not substitute publication time: %v", err)
	}
}

func TestBonusReadsAreAuthenticatedAndCacheOnlyImmutableMetadata(t *testing.T) {
	t.Parallel()
	details, lists := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(authTokenCookie)
		if err != nil || cookie.Value != "fixture-auth" || r.Method != http.MethodGet {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/mybonus.php":
			if r.URL.Query().Get("show") != "seed" {
				t.Error("wrong bonus query")
			}
			fmt.Fprint(w, bonusPageFixture)
		case "/getusertorrentlistajax.php":
			if r.URL.Query().Get("userid") != "42" || r.URL.Query().Get("type") != "seeding" {
				t.Error("wrong seeding query")
			}
			lists++
			fmt.Fprint(w, bonusSeedingFixture)
		case "/details.php":
			if r.URL.Query().Get("id") != "123" {
				t.Error("wrong torrent ID")
			}
			details++
			fmt.Fprint(w, bonusMetadataFixture)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	auth, err := NewAuthenticator(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client, err := auth.NewClient(Credential{AuthToken: "fixture-auth"})
	if err != nil {
		t.Fatal(err)
	}
	tracker := netretry.NewBonusTracker(client, netretry.Policy{})
	for i := 0; i < 2; i++ {
		account, err := tracker.BonusAccount(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tracker.ListSeeding(context.Background(), account.UserID); err != nil {
			t.Fatal(err)
		}
		if _, err := tracker.TorrentMetadata(context.Background(), domain.TorrentID("123")); err != nil {
			t.Fatal(err)
		}
	}
	if lists != 2 || details != 1 {
		t.Fatalf("seeding reads = %d, metadata reads = %d", lists, details)
	}
}
