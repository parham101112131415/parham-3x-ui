package service

import (
	"net"
	"os"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// adult_site.go decides whether a visited host is an adult (18+)
// destination, for the visited-sites labels and the Telegram adult-site
// watcher. Two nets, strongest first:
//
//  1. A baked-in list of ~1.16M known adult domains (merged at image
//     build time from the BlocklistProject porn list, the oisd nsfw list
//     and Steven Black's porn-only list — see the Dockerfile). Matching
//     is by suffix: a host matches when the host itself or any parent
//     domain is listed, so cdn.xvideos.com matches xvideos.com.
//  2. Heuristics for domains too new or obscure to be listed yet:
//     dedicated adult TLDs (.xxx, .porn, …) and conservative keyword
//     labels (a label equal to, starting with, or — for porn/sex —
//     ending with an adult keyword).
//
// A bare IP can never be classified: there is no name to judge. That is
// inherent to how the proxy sees destinations, not a list gap.

// adultSitesFile is where the image bakes the merged domain list; the
// env var mirrors PARHAM_GEOLITE2_CITY_DB's pattern.
const adultSitesDefaultPath = "/usr/local/share/parham/adult_domains.txt"

var adultDomainList atomic.Pointer[[]string]

func loadAdultDomains() []string {
	if p := adultDomainList.Load(); p != nil {
		return *p
	}
	path := os.Getenv("PARHAM_ADULT_SITES_FILE")
	if path == "" {
		path = adultSitesDefaultPath
	}
	list := []string{}
	if raw, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				list = append(list, line)
			}
		}
		sort.Strings(list)
	} else {
		logger.Warning("[AdultSite] domain list unavailable, heuristics only: ", err)
	}
	if adultDomainList.CompareAndSwap(nil, &list) {
		return list
	}
	return *adultDomainList.Load()
}

// SetAdultDomainsForTest replaces the loaded list (tests only).
func SetAdultDomainsForTest(list []string) {
	sorted := append([]string{}, list...)
	sort.Strings(sorted)
	adultDomainList.Store(&sorted)
}

func adultListContains(domain string) bool {
	list := loadAdultDomains()
	i := sort.SearchStrings(list, domain)
	return i < len(list) && list[i] == domain
}

var adultTLDs = map[string]bool{
	"xxx": true, "porn": true, "porno": true, "adult": true, "sex": true,
	"sexy": true, "erotic": true, "webcam": true, "tube": true,
}

// adultWholeLabels match a whole dot-label exactly.
var adultWholeLabels = map[string]bool{
	"porn": true, "porno": true, "sex": true, "xxx": true, "nsfw": true,
	"erotic": true, "hentai": true, "nude": true, "milf": true,
}

// adultLabelPrefixes match a label that starts with the keyword and is
// longer than it (pornhub, sexvideos, hentairead, …).
var adultLabelPrefixes = []string{"porn", "sex", "hentai"}

// sexSuffixAllowlist holds place-name labels that end in "sex" without
// being adult (Essex, Sussex, Middlesex).
var sexSuffixAllowlist = map[string]bool{
	"essex": true, "sussex": true, "middlesex": true,
}

// multiPartSuffixes are second-level suffixes under which the
// registrable domain has three labels (co.uk, com.au, …).
var multiPartSuffixes = map[string]bool{
	"co.uk": true, "org.uk": true, "ac.uk": true, "gov.uk": true,
	"com.au": true, "net.au": true, "org.au": true, "co.nz": true,
	"com.br": true, "com.cn": true, "com.tw": true, "com.hk": true,
	"co.jp": true, "ne.jp": true, "or.jp": true, "co.kr": true,
	"co.in": true, "com.mx": true, "com.tr": true, "co.za": true,
	"co.ir": true, "ac.ir": true, "co.id": true, "co.th": true,
	"co.il": true, "com.sg": true, "com.my": true, "com.vn": true,
	"com.ph": true, "com.pk": true, "com.ar": true, "com.co": true,
	"com.pl": true, "com.ua": true, "com.ng": true, "co.ke": true,
}

// registrableDomain returns the eTLD+1 of a host's labels.
func registrableDomain(labels []string) string {
	if len(labels) <= 2 {
		return strings.Join(labels, ".")
	}
	last2 := labels[len(labels)-2] + "." + labels[len(labels)-1]
	if multiPartSuffixes[last2] {
		return strings.Join(labels[len(labels)-3:], ".")
	}
	return last2
}

// AdultSiteIdentity reports whether a visited host is an adult
// destination, and if so the site's identity: the listed domain that
// matched, or — for heuristic matches — the shortest host suffix that
// contains the offending label (www.sexvideos.com → sexvideos.com).
func AdultSiteIdentity(host string) (string, bool) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return "", false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if net.ParseIP(host) != nil {
		return "", false
	}

	// Net 1: the baked-in list, by suffix.
	for candidate := host; ; {
		if adultListContains(candidate) {
			return candidate, true
		}
		i := strings.Index(candidate, ".")
		if i < 0 {
			break
		}
		candidate = candidate[i+1:]
	}

	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", false
	}

	// Net 2a: adult TLD.
	if adultTLDs[labels[len(labels)-1]] {
		return registrableDomain(labels), true
	}

	// Net 2b: keyword labels. Scan right-to-left (skipping the TLD) so
	// the identity suffix starts at the offending label nearest the
	// registrable domain.
	for i := len(labels) - 2; i >= 0; i-- {
		if adultLabelMatches(labels[i]) {
			return strings.Join(labels[i:], "."), true
		}
	}
	return "", false
}

func adultLabelMatches(label string) bool {
	if adultWholeLabels[label] {
		return true
	}
	for _, prefix := range adultLabelPrefixes {
		if len(label) > len(prefix) && strings.HasPrefix(label, prefix) {
			return true
		}
	}
	if len(label) > 4 && strings.HasSuffix(label, "porn") {
		return true
	}
	if len(label) > 4 && strings.HasSuffix(label, "sex") && !sexSuffixAllowlist[label] {
		return true
	}
	return false
}
