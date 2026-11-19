package service

import (
	"net"
	"strings"
)

// site_service.go labels a visited host with the well-known service it
// belongs to, when it belongs to one. The Xray access log only carries
// the destination the client connected to — never which app on the
// device opened the connection (the VPN protocols have no such field).
// For the big services the destination itself is distinctive enough:
// graph.instagram.com is Instagram whether the Instagram app or a
// browser asked for it, and Telegram's DC addresses are Telegram's.
// Generic hosts (an ordinary website) get no label, and the panel shows
// the bare host for them — a label is a display aid, not a claim about
// which app was used.

// siteDomainRules maps a domain suffix to its service. Matching is by
// exact host or dot-boundary suffix; the longest matching suffix wins,
// so youtubei.googleapis.com is YouTube, not generic Google.
var siteDomainRules = []struct {
	suffix  string
	service string
}{
	{"instagram.com", "Instagram"},
	{"cdninstagram.com", "Instagram"},
	{"whatsapp.net", "WhatsApp"},
	{"whatsapp.com", "WhatsApp"},
	{"telegram.org", "Telegram"},
	{"telegram.me", "Telegram"},
	{"t.me", "Telegram"},
	{"youtubei.googleapis.com", "YouTube"},
	{"youtube.com", "YouTube"},
	{"googlevideo.com", "YouTube"},
	{"ytimg.com", "YouTube"},
	{"tiktokv.com", "TikTok"},
	{"tiktok.com", "TikTok"},
	{"tiktokcdn.com", "TikTok"},
	{"byteoversea.com", "TikTok"},
	{"ibytedtos.com", "TikTok"},
	{"facebook.com", "Facebook"},
	{"fbcdn.net", "Facebook"},
	{"messenger.com", "Facebook"},
	{"twitter.com", "X (Twitter)"},
	{"x.com", "X (Twitter)"},
	{"twimg.com", "X (Twitter)"},
	{"spotify.com", "Spotify"},
	{"scdn.co", "Spotify"},
	{"netflix.com", "Netflix"},
	{"nflxvideo.net", "Netflix"},
	{"nflximg.net", "Netflix"},
	{"discord.com", "Discord"},
	{"discordapp.com", "Discord"},
	{"discordapp.net", "Discord"},
	{"snapchat.com", "Snapchat"},
	{"sc-cdn.net", "Snapchat"},
	{"pinterest.com", "Pinterest"},
	{"pinimg.com", "Pinterest"},
	{"reddit.com", "Reddit"},
	{"redditstatic.com", "Reddit"},
	{"redd.it", "Reddit"},
	{"linkedin.com", "LinkedIn"},
	{"licdn.com", "LinkedIn"},
	{"twitch.tv", "Twitch"},
	{"twitchcdn.net", "Twitch"},
	{"google.com", "Google"},
	{"gstatic.com", "Google"},
	{"ggpht.com", "Google"},
	{"googleusercontent.com", "Google"},
	{"googleapis.com", "Google"},
	{"apple.com", "Apple"},
	{"icloud.com", "Apple"},
	{"mzstatic.com", "Apple"},
	{"microsoft.com", "Microsoft"},
	{"live.com", "Microsoft"},
	{"office.com", "Microsoft"},
	{"bing.com", "Microsoft"},
	{"chatgpt.com", "ChatGPT"},
	{"openai.com", "ChatGPT"},
	{"github.com", "GitHub"},
	{"githubusercontent.com", "GitHub"},
	{"cloudflare.com", "Cloudflare"},
	{"aparat.com", "Aparat"},
	{"digikala.com", "Digikala"},
	{"divar.ir", "Divar"},
	{"rubika.ir", "Rubika"},
	{"eitaa.com", "Eitaa"},
	{"bale.ai", "Bale"},
}

// classifySiteHost returns the service label for a visited host, or ""
// when the host is not a known service destination.
func classifySiteHost(host string) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		return classifySiteIP(ip)
	}
	best, bestLen := "", -1
	for _, rule := range siteDomainRules {
		if host == rule.suffix || strings.HasSuffix(host, "."+rule.suffix) {
			if len(rule.suffix) > bestLen {
				best, bestLen = rule.service, len(rule.suffix)
			}
		}
	}
	if best == "" {
		if _, ok := AdultSiteIdentity(host); ok {
			return "🔞 Adult"
		}
	}
	return best
}

// classifySiteIP labels bare-IP destinations. Only Telegram's data
// centre ranges are distinctive enough to label: Telegram clients very
// often connect to DC addresses directly, with no domain at all.
func classifySiteIP(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		// 149.154.160.0/20 — Telegram DCs (Amsterdam).
		if v4[0] == 149 && v4[1] == 154 && v4[2] >= 160 && v4[2] <= 175 {
			return "Telegram"
		}
		// 91.108.x.x Telegram DC blocks: /22s at 4, 8, 12, 16, 20, 56.
		if v4[0] == 91 && v4[1] == 108 {
			switch third := v4[2]; {
			case third >= 4 && third <= 23 && third%4 <= 3:
				return "Telegram"
			case third >= 56 && third <= 59:
				return "Telegram"
			}
		}
		return ""
	}
	s := ip.String()
	// Telegram DC IPv6 space (e.g. 2001:b28:f23d:f001::a is DC1).
	if strings.HasPrefix(s, "2001:b28:f23") || strings.HasPrefix(s, "2001:67c:4e8:") {
		return "Telegram"
	}
	return ""
}
