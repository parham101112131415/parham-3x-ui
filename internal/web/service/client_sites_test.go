package service

import (
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestParseAccessLogLine(t *testing.T) {
	// Real lines captured from Xray v26.10.21 with a VLESS inbound whose
	// user carries an email (see client_sites.go).
	email, host, ts, ok := parseAccessLogLine("2026/11/19 10:48:12.547361 from 127.0.0.1:48488 accepted tcp:example.com:443 [vless-in >> direct] email: testuser")
	if !ok || email != "testuser" || host != "example.com" {
		t.Fatalf("domain line: ok=%v email=%q host=%q", ok, email, host)
	}
	if want := time.Date(2026, 11, 19, 10, 48, 12, 0, time.Local).Unix(); ts != want {
		t.Fatalf("timestamp: got %d want %d", ts, want)
	}

	_, host, _, ok = parseAccessLogLine("2026/11/19 10:48:27.558902 from 127.0.0.1:36358 accepted tcp:www.wikipedia.org:443 [vless-in >> direct] email: testuser")
	if !ok || host != "www.wikipedia.org" {
		t.Fatalf("second line: ok=%v host=%q", ok, host)
	}

	// IP-literal destination keeps the address as the host.
	_, host, _, ok = parseAccessLogLine("2026/11/19 11:00:00.000001 from 10.0.0.2:1000 accepted tcp:185.199.108.153:443 [vless-in >> direct] email: bob")
	if !ok || host != "185.199.108.153" {
		t.Fatalf("ip line: ok=%v host=%q", ok, host)
	}

	// Rejections and non-connection lines never count.
	for _, line := range []string{
		"2026/11/19 11:00:00.000001 from 10.0.0.2:1000 rejected tcp:example.com:443 [vless-in >> direct] email: bob",
		"2026/11/19 11:00:00.000001 [Info] app/dns: returning 1 records",
		"2026/11/19 11:00:00.000001 from 10.0.0.2:1000 accepted tcp:example.com:443 [vless-in >> direct]",
		"",
	} {
		if _, _, _, ok := parseAccessLogLine(line); ok {
			t.Fatalf("line should not parse: %q", line)
		}
	}
}

func TestMergeClientSiteEntries(t *testing.T) {
	now := time.Now().Unix()
	cutoff := now - clientSiteRetentionSeconds
	old := []model.ClientSiteEntry{
		{Host: "a.com", Count: 3, LastSeen: now - 100},
		{Host: "stale.com", Count: 9, LastSeen: cutoff - 10},
	}
	incoming := []model.ClientSiteEntry{
		{Host: "a.com", Count: 2, LastSeen: now},
		{Host: "b.com", Count: 1, LastSeen: now - 50},
	}
	merged := mergeClientSiteEntries(old, incoming, cutoff)
	if len(merged) != 2 {
		t.Fatalf("expected 2 hosts after merge, got %+v", merged)
	}
	// Busiest first: a.com with the summed count.
	if merged[0].Host != "a.com" || merged[0].Count != 5 || merged[0].LastSeen != now {
		t.Fatalf("unexpected merge head: %+v", merged[0])
	}
	if merged[1].Host != "b.com" || merged[1].Count != 1 {
		t.Fatalf("unexpected merge tail: %+v", merged[1])
	}
}
