package byr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/netretry"
)

func TestParseTopFree(t *testing.T) {
	t.Parallel()

	candidates, err := parseTopFree(strings.NewReader(torrentPageFixture("1.50 GiB", "12", "3")))
	if err != nil {
		t.Fatalf("parseTopFree() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}

	candidate := candidates[0]
	if candidate.ID != "362950" {
		t.Errorf("ID = %q, want %q", candidate.ID, "362950")
	}
	if candidate.Name != "Example TopFree" {
		t.Errorf("Name = %q, want %q", candidate.Name, "Example TopFree")
	}
	if candidate.SizeBytes != 1610612736 {
		t.Errorf("SizeBytes = %d, want 1610612736", candidate.SizeBytes)
	}
	if candidate.Seeders != 12 || candidate.Leechers != 3 {
		t.Errorf("peers = (%d, %d), want (12, 3)", candidate.Seeders, candidate.Leechers)
	}
	wantDeadline := time.Date(2026, time.August, 12, 14, 56, 28, 0, byrLocation)
	if !candidate.FreeUntil.Equal(wantDeadline) {
		t.Errorf("FreeUntil = %s, want %s", candidate.FreeUntil, wantDeadline)
	}
}

func TestParseTopFreeRejectsMalformedCandidate(t *testing.T) {
	t.Parallel()

	_, err := parseTopFree(strings.NewReader(torrentPageFixture("unknown", "12", "3")))
	if !errors.Is(err, ErrInvalidTorrentPage) {
		t.Fatalf("error = %v, want ErrInvalidTorrentPage", err)
	}
}

func TestParseTopFreeRequiresTorrentTable(t *testing.T) {
	t.Parallel()

	_, err := parseTopFree(strings.NewReader("<html><body>ok</body></html>"))
	if !errors.Is(err, ErrInvalidTorrentPage) {
		t.Fatalf("error = %v, want ErrInvalidTorrentPage", err)
	}
}

func TestPageParsersPreserveBodyReadTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		parse   func() error
		wantErr error
	}{
		{name: "TopFree", parse: func() error { _, err := parseTopFree(deadlineReader{}); return err }, wantErr: ErrInvalidTorrentPage},
		{name: "torrent hash", parse: func() error { _, err := parseTorrentHash(deadlineReader{}); return err }, wantErr: ErrTorrentHashNotFound},
		{name: "passkey", parse: func() error { _, err := parsePasskey(deadlineReader{}); return err }, wantErr: ErrPasskeyNotFound},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.parse()
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if !errors.Is(err, context.DeadlineExceeded) || !netretry.IsTransient(err) {
				t.Fatalf("body read timeout was not preserved as transient: %v", err)
			}
		})
	}
}

type deadlineReader struct{}

func (deadlineReader) Read([]byte) (int, error) {
	return 0, context.DeadlineExceeded
}

func TestParseSizeBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  int64
	}{
		{input: "512 B", want: 512},
		{input: "2 KiB", want: 2 << 10},
		{input: "1.5 MiB", want: 1572864},
		{input: "3 GiB", want: 3 << 30},
		{input: "1 TiB", want: 1 << 40},
	}
	for _, test := range tests {
		test := test
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()
			got, err := parseSizeBytes(test.input)
			if err != nil {
				t.Fatalf("parseSizeBytes() error = %v", err)
			}
			if got != test.want {
				t.Errorf("parseSizeBytes() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestParseTorrentHash(t *testing.T) {
	t.Parallel()

	const uppercaseHash = "0123456789ABCDEF0123456789ABCDEF01234567"
	got, err := parseTorrentHash(strings.NewReader("<table><tr><td>Hash码</td><td>" + uppercaseHash + "</td></tr></table>"))
	if err != nil {
		t.Fatalf("parseTorrentHash() error = %v", err)
	}
	if got != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("hash = %q", got)
	}
}

func TestParseTorrentHashRequiresLabel(t *testing.T) {
	t.Parallel()

	_, err := parseTorrentHash(strings.NewReader("<p>0123456789abcdef0123456789abcdef01234567</p>"))
	if !errors.Is(err, ErrTorrentHashNotFound) {
		t.Fatalf("error = %v, want ErrTorrentHashNotFound", err)
	}
}

func TestParsePasskey(t *testing.T) {
	t.Parallel()

	const passkey = "0123456789abcdef0123456789abcdef"
	got, err := parsePasskey(strings.NewReader("<table><tr><td>passkey</td><td><span>" + passkey + "</span><button>复制</button></td></tr></table>"))
	if err != nil {
		t.Fatalf("parsePasskey() error = %v", err)
	}
	if got != passkey {
		t.Errorf("passkey = %q, want fixture value", got)
	}
}

func TestParsePasskeyRequiresValueCell(t *testing.T) {
	t.Parallel()

	_, err := parsePasskey(strings.NewReader("<table><tr><td>passkey</td></tr></table>"))
	if !errors.Is(err, ErrPasskeyNotFound) {
		t.Fatalf("error = %v, want ErrPasskeyNotFound", err)
	}
}

func torrentPageFixture(size, seeders, leechers string) string {
	return `<html><body><table class="torrents coltable full">
<tr>
  <td>category</td>
  <td><table><tr><td>
    <div class="icons sticky-buy inverse-y" title="竞价置顶至2026-08-12 14:56:28"></div>
    <a href="details.php?id=362950&amp;hit=1" title="Example TopFree"><span>Example TopFree</span></a>
    <img class="pro_free" src="pic/trans.gif">
  </td></tr></table></td>
  <td>comments</td><td>time</td><td>uploader</td>
  <td>` + size + `</td><td>` + seeders + `</td><td>` + leechers + `</td><td>done</td>
</tr>
<tr>
  <td>category</td>
  <td><div class="sticky-one" title="置顶至2026-08-13 10:00:00"></div><a href="details.php?id=2" title="Sticky only">Sticky only</a></td>
  <td></td><td></td><td></td><td>1 GiB</td><td>1</td><td>0</td>
</tr>
<tr>
  <td>category</td>
  <td><a href="details.php?id=3" title="Free only">Free only</a><img class="pro_free2up"></td>
  <td></td><td></td><td></td><td>1 GiB</td><td>1</td><td>0</td>
</tr>
</table></body></html>`
}
