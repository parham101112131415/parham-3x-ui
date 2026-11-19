package job

import (
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func TestFoldLiveClientIpsWindowAndDedupe(t *testing.T) {
	now := int64(1_000_000)
	rows := []model.NodeClientIp{
		{NodeGuid: "g1", Email: "a@x", Ips: `[{"ip":"1.1.1.1","timestamp":999950},{"ip":"2.2.2.2","timestamp":999000}]`},
		{NodeGuid: "g2", Email: "a@x", Ips: `[{"ip":"3.3.3.3","timestamp":999990},{"ip":"1.1.1.1","timestamp":999995}]`},
		{NodeGuid: "g1", Email: "b@x", Ips: `[{"ip":"4.4.4.4","timestamp":999990}]`},
		{NodeGuid: "g1", Email: "bad@x", Ips: `not json`},
	}
	live := service.FoldLiveClientIps(rows, now-shareLiveWindowSeconds)
	if len(live["a@x"]) != 2 {
		t.Fatalf("a@x: want 2 fresh distinct IPs (stale 2.2.2.2 dropped, dupe 1.1.1.1 folded), got %+v", live["a@x"])
	}
	if len(live["b@x"]) != 1 {
		t.Fatalf("b@x: want 1 IP, got %+v", live["b@x"])
	}
	if _, ok := live["bad@x"]; ok {
		t.Fatal("bad@x: unparseable row must be skipped")
	}
}

func TestShareAlertText(t *testing.T) {
	text := shareAlertText("a@x",
		[]service.AttributedIp{{IP: "1.1.1.1", Guid: "g1"}, {IP: "3.3.3.3", Guid: "g2"}},
		map[string]string{"g1": "us-west", "g2": "asia"})
	for _, want := range []string{"a@x", "us-west", "asia", "1.1.1.1", "3.3.3.3"} {
		if !strings.Contains(text, want) {
			t.Fatalf("alert text missing %q:\n%s", want, text)
		}
	}
}
