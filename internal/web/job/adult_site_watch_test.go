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

func TestAdultSiteWatch_BaselineThenNewVisitsWithCooldown(t *testing.T) {
	service.SetAdultDomainsForTest([]string{"xvideos.com"})
	j := NewAdultSiteWatchJob()
	now := time.Unix(1_700_000_000, 0)

	// Baseline pass: pre-existing visits seed the watermarks, no alerts.
	rows := []model.NodeClientSite{
		adultRow("g-eu", "nima",
			model.ClientSiteEntry{Host: "www.xvideos.com", Count: 5, LastSeen: 1000},
			model.ClientSiteEntry{Host: "google.com", Count: 9, LastSeen: 1000},
		),
	}
	if hits := j.scan(rows, now); len(hits) != 0 {
		t.Fatalf("baseline pass must not alert, got %v", hits)
	}
	// Same data again: watermarks already cover it.
	if hits := j.scan(rows, now); len(hits) != 0 {
		t.Fatalf("unchanged data must not alert, got %v", hits)
	}

	// A fresh visit (LastSeen advanced) alerts once, with the listed
	// domain as the site identity.
	rows[0] = adultRow("g-eu", "nima",
		model.ClientSiteEntry{Host: "www.xvideos.com", Count: 6, LastSeen: 2000},
		model.ClientSiteEntry{Host: "google.com", Count: 10, LastSeen: 2000},
	)
	hits := j.scan(rows, now)
	if len(hits) != 1 || hits[0].Email != "nima" || hits[0].Guid != "g-eu" || hits[0].Site != "xvideos.com" {
		t.Fatalf("expected one xvideos.com hit for nima, got %v", hits)
	}

	// More visits inside the cooldown: no repeat alert.
	rows[0] = adultRow("g-eu", "nima",
		model.ClientSiteEntry{Host: "www.xvideos.com", Count: 8, LastSeen: 3000},
	)
	if hits := j.scan(rows, now.Add(time.Hour)); len(hits) != 0 {
		t.Fatalf("cooldown must suppress repeat alerts, got %v", hits)
	}

	// After the cooldown, a new visit alerts again.
	rows[0] = adultRow("g-eu", "nima",
		model.ClientSiteEntry{Host: "www.xvideos.com", Count: 9, LastSeen: 4000},
	)
	if hits := j.scan(rows, now.Add(adultAlertCooldown+time.Hour)); len(hits) != 1 {
		t.Fatalf("visit after cooldown must alert again, got %v", hits)
	}
}

func TestAdultAlertText(t *testing.T) {
	text := adultAlertText("nima", "eu-west", []adultSiteHit{{Email: "nima", Guid: "g-eu", Site: "xvideos.com"}})
	for _, want := range []string{"🔞", "nima", "eu-west", "xvideos.com"} {
		if !strings.Contains(text, want) {
			t.Errorf("alert text %q missing %q", text, want)
		}
	}
}
