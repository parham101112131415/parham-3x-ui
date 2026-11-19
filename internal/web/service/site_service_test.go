package service

import "testing"

func TestClassifySiteHost(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"graph.instagram.com", "Instagram"},
		{"scontent.cdninstagram.com", "Instagram"},
		{"instagram.com", "Instagram"},
		{"e15.whatsapp.net", "WhatsApp"},
		{"api.telegram.org", "Telegram"},
		{"www.youtube.com", "YouTube"},
		{"youtubei.googleapis.com", "YouTube"}, // longest suffix beats googleapis.com
		{"rr5---sn-2on4v5-5p.googlevideo.com", "YouTube"},
		{"www.google.com", "Google"},
		{"fonts.googleapis.com", "Google"},
		{"api16-normal-c-useast1a.tiktokv.com", "TikTok"},
		{"149.154.167.99", "Telegram"},
		{"149.154.160.1", "Telegram"},
		{"91.108.56.150", "Telegram"},
		{"91.108.5.10", "Telegram"},
		{"91.108.24.10", ""}, // just outside the Telegram /22 blocks
		{"2001:b28:f23d:f001::a", "Telegram"},
		{"push.vasl.social", ""},
		{"example.com", ""},
		{"notinstagram.com", ""}, // no dot boundary, no match
		{"", ""},
	}
	for _, tc := range cases {
		if got := classifySiteHost(tc.host); got != tc.want {
			t.Errorf("classifySiteHost(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}
