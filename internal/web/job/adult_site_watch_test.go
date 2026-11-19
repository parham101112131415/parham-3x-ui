package job

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func adultRow(guid, email string, entries ...model.ClientSiteEntry) model.NodeClientSite {
	raw, _ := json.Marshal(entries)
	return model.NodeClientSite{NodeGuid: guid, Email: email, Sites: string(raw)}
}

func TestAdultSiteWatch_BaselineThenPerEntryAlerts(t *testing.T) {
	service.SetAdultDomainsForTest([]string{"xvideos.com", "pornhub.com"})
	j := NewAdultSiteWatchJob()

	// Baseline pass: pre-existing visits seed the watermarks, no alerts.
	rows := []model.NodeClientSite{
		adultRow("g-eu", "nima",
			model.ClientSiteEntry{Host: "www.xvideos.com", Count: 5, LastSeen: 1000},
			model.ClientSiteEntry{Host: "google.com", Count: 9, LastSeen: 1000},
		),
	}
	if hits := j.scan(rows); len(hits) != 0 {
		t.Fatalf("baseline pass must not alert, got %v", hits)
	}
	// Same data again: watermarks already cover it.
	if hits := j.scan(rows); len(hits) != 0 {
		t.Fatalf("unchanged data must not alert, got %v", hits)
	}

	// A new entry (activity resumes long after the seeded record)
	// alerts at once, carrying the full host as visited and its time.
	rows[0] = adultRow("g-eu", "nima",
		model.ClientSiteEntry{Host: "www.xvideos.com", Count: 6, LastSeen: 2000},
		model.ClientSiteEntry{Host: "google.com", Count: 10, LastSeen: 2000},
	)
	hits := j.scan(rows)
	if len(hits) != 1 || hits[0].Email != "nima" || hits[0].Guid != "g-eu" ||
		hits[0].Site != "www.xvideos.com" || hits[0].When != 2000 {
		t.Fatalf("expected one www.xvideos.com hit for nima at 2000, got %v", hits)
	}

	// Continuous activity inside the same visit (lastSeen advancing in
	// small steps) does not re-alert: it is one entry, already told.
	rows[0] = adultRow("g-eu", "nima",
		model.ClientSiteEntry{Host: "www.xvideos.com", Count: 8, LastSeen: 2060},
	)
	if hits := j.scan(rows); len(hits) != 0 {
		t.Fatalf("continuous visit must not re-alert, got %v", hits)
	}
	rows[0] = adultRow("g-eu", "nima",
		model.ClientSiteEntry{Host: "www.xvideos.com", Count: 11, LastSeen: 2150},
	)
	if hits := j.scan(rows); len(hits) != 0 {
		t.Fatalf("continuous visit must not re-alert, got %v", hits)
	}

	// Leaving and coming back (a gap larger than one visit) is a new
	// entry and alerts again immediately — no cooldown.
	rows[0] = adultRow("g-eu", "nima",
		model.ClientSiteEntry{Host: "www.xvideos.com", Count: 12, LastSeen: 3000},
	)
	hits = j.scan(rows)
	if len(hits) != 1 || hits[0].When != 3000 {
		t.Fatalf("re-entry after a gap must alert again, got %v", hits)
	}

	// A first-ever visit to another adult host alerts too (a client is
	// covered automatically, no per-client setup).
	rows[0] = adultRow("g-eu", "nima",
		model.ClientSiteEntry{Host: "www.xvideos.com", Count: 12, LastSeen: 3000},
		model.ClientSiteEntry{Host: "www.pornhub.com", Count: 1, LastSeen: 3100},
	)
	hits = j.scan(rows)
	if len(hits) != 1 || hits[0].Site != "www.pornhub.com" || hits[0].When != 3100 {
		t.Fatalf("first visit to a new adult host must alert, got %v", hits)
	}
}

func TestAdultAlertText(t *testing.T) {
	// 1700000000 = 2023-11-14 22:13:20 UTC.
	text := adultAlertText("nima", "eu-west",
		[]adultSiteHit{{Email: "nima", Guid: "g-eu", Site: "www.xvideos.com", When: 1_700_000_000}},
		time.UTC)
	for _, want := range []string{"🔞", "nima", "eu-west", "www.xvideos.com", "22:13:20", "2023-11-14"} {
		if !strings.Contains(text, want) {
			t.Errorf("alert text %q missing %q", text, want)
		}
	}
}
