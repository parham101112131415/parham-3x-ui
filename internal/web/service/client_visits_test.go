package service

import "testing"

func TestParseDeviceLabel(t *testing.T) {
	cases := []struct {
		ua   string
		want string
	}{
		{"Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36", "SM-S918B · Android 14 · Chrome"},
		{"Mozilla/5.0 (Linux; Android 13; Pixel 7 Build/TQ3A.230901.001) AppleWebKit/537.36 Chrome/119.0.0.0 Mobile Safari/537.36", "Pixel 7 · Android 13 · Chrome"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Mobile/15E148 Safari/604.1", "iPhone · iOS 17.2 · Safari"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0", "Windows 10.0 · Edge"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_2_1) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15", "macOS 14.2.1 · Safari"},
		{"", ""},
		{"curl/8.5.0", "curl/8.5.0"},
	}
	for _, tc := range cases {
		if got := ParseDeviceLabel(tc.ua); got != tc.want {
			t.Errorf("ParseDeviceLabel(%q) = %q, want %q", tc.ua, got, tc.want)
		}
	}
}
