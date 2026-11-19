package service

import "testing"

func TestAdultSiteIdentity(t *testing.T) {
	SetAdultDomainsForTest([]string{"xvideos.com", "pornhub.com", "onlyfans.com"})

	cases := []struct {
		host string
		want string
		ok   bool
	}{
		// Baked-in list: exact and suffix matches.
		{"xvideos.com", "xvideos.com", true},
		{"www.xvideos.com", "xvideos.com", true},
		{"img.cdn.xvideos.com", "xvideos.com", true},
		{"onlyfans.com:443", "onlyfans.com", true},
		// Adult TLDs.
		{"cool-videos.porn", "cool-videos.porn", true},
		{"example.xxx", "example.xxx", true},
		{"videos.example.tube", "example.tube", true},
		// Keyword labels.
		{"www.pornhubclone.com", "pornhubclone.com", true},
		{"sex.example.com", "sex.example.com", true},
		{"www.sexvideos.com", "sexvideos.com", true},
		{"freeporn.net", "freeporn.net", true},
		{"hentaimax.org", "hentaimax.org", true},
		// Not adult: mainstream, place names, bare IPs, near misses.
		{"google.com", "", false},
		{"www.youtube.com", "", false},
		{"sussex.ac.uk", "", false},
		{"essex.com", "", false},
		{"middlesex.edu", "", false},
		{"91.108.56.149", "", false},
		{"sextant-instruments.com", "sextant-instruments.com", true}, // "sex" prefix rule: known, accepted false-positive class
		{"example.com", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := AdultSiteIdentity(c.host)
		if ok != c.ok || got != c.want {
			t.Errorf("AdultSiteIdentity(%q) = (%q, %v), want (%q, %v)", c.host, got, ok, c.want, c.ok)
		}
	}
}

// The 🔞 label shows up through the same classifier the visited-sites
// section uses.
func TestClassifySiteHostAdultLabel(t *testing.T) {
	SetAdultDomainsForTest([]string{"xvideos.com"})
	if got := classifySiteHost("cdn.xvideos.com"); got != "🔞 Adult" {
		t.Errorf("classifySiteHost(cdn.xvideos.com) = %q, want 🔞 Adult", got)
	}
	if got := classifySiteHost("graph.instagram.com"); got != "Instagram" {
		t.Errorf("classifySiteHost(graph.instagram.com) = %q, want Instagram", got)
	}
}

func TestAdultSiteIdentity_ExcludedDomainsWinOverList(t *testing.T) {
	// mob.com sits in the baked list (a BlocklistProject false
	// positive) but is an app-SDK domain: the exclusion must win for
	// the domain itself and every subdomain, while real listed adult
	// domains keep matching.
	SetAdultDomainsForTest([]string{"mob.com", "xvideos.com"})
	for _, host := range []string{"mob.com", "api-share.mob.com", "a.b.mob.com"} {
		if site, ok := AdultSiteIdentity(host); ok {
			t.Errorf("AdultSiteIdentity(%q) = %q, true; excluded domain must not classify", host, site)
		}
	}
	if _, ok := AdultSiteIdentity("www.xvideos.com"); !ok {
		t.Error("www.xvideos.com must still classify as adult")
	}
}
