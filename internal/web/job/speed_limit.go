package job

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// Per-client speed limiting. Xray has no per-user rate limiter, so the panel
// enforces a client's limitSpeed (bytes/sec) as an average rate with a token
// bucket fed by the 1-second traffic deltas: while a client is over budget
// its user is removed from the local Xray inbounds (new connections fail,
// which is what throttles it); when the bucket has repaid the debt plus a
// small hysteresis margin, the user is re-added. The bucket allows a burst
// of speedBurstSeconds worth of traffic so short spikes are not punished.
const (
	speedBurstSeconds  = 5.0
	speedResumeSeconds = 1.0
)

type speedBucket struct {
	tokens    float64
	suspended bool
	tags      []string
}

type speedLimiter struct {
	mu      sync.Mutex
	buckets map[string]*speedBucket
	lastRun time.Time
}

func newSpeedLimiter() *speedLimiter {
	return &speedLimiter{buckets: map[string]*speedBucket{}}
}

// enforce advances every limited client's bucket by this poll's deltas and
// suspends/resumes users whose budget state flipped.
func (l *speedLimiter) enforce(deltas []*xray.ClientTraffic) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	dt := 1.0
	if !l.lastRun.IsZero() {
		dt = now.Sub(l.lastRun).Seconds()
		if dt < 0 {
			dt = 0
		}
		if dt > 10 {
			dt = 10
		}
	}
	l.lastRun = now

	usage := make(map[string]int64, len(deltas))
	for _, ct := range deltas {
		if ct != nil {
			usage[ct.Email] += ct.Up + ct.Down
		}
	}

	limits := loadSpeedLimits()
	if len(limits) == 0 && len(l.buckets) == 0 {
		return
	}

	type action struct {
		email  string
		resume bool
	}
	var actions []action

	for email, limit := range limits {
		b := l.buckets[email]
		if b == nil {
			b = &speedBucket{tokens: float64(limit) * speedBurstSeconds}
			l.buckets[email] = b
		}
		switch bucketStep(b, limit, dt, usage[email]) {
		case 1:
			actions = append(actions, action{email: email})
		case -1:
			actions = append(actions, action{email: email, resume: true})
		}
	}
	// A client whose limit was removed while suspended comes back at once.
	for email, b := range l.buckets {
		if _, ok := limits[email]; ok {
			continue
		}
		if b.suspended {
			actions = append(actions, action{email: email, resume: true})
		}
		delete(l.buckets, email)
	}
	if len(actions) == 0 {
		return
	}

	var api xray.XrayAPI
	if err := api.Init(resolveSpeedAPIPort()); err != nil {
		logger.Warning("[SpeedLimit] xray API init failed: ", err)
		return
	}
	defer api.Close()

	localInbounds := loadLocalInbounds()
	for _, a := range actions {
		b := l.buckets[a.email]
		if a.resume {
			tags := b.tags
			if b == nil || len(tags) == 0 {
				// Bucket state was dropped; re-add everywhere the client is.
				tags = nil
			}
			resumeClient(&api, localInbounds, a.email, tags)
			if b != nil {
				b.suspended = false
				b.tags = nil
			}
			continue
		}
		removed := suspendClient(&api, localInbounds, a.email)
		if b != nil {
			b.suspended = true
			b.tags = removed
		}
	}
}

// bucketStep advances one bucket by dt seconds with `used` bytes consumed in
// the interval. It returns 1 when the client must be suspended (budget newly
// exhausted), -1 when a suspended client must be resumed (debt repaid plus
// the hysteresis margin), and 0 when nothing changes.
func bucketStep(b *speedBucket, limit int64, dt float64, used int64) int {
	capacity := float64(limit) * speedBurstSeconds
	b.tokens += float64(limit) * dt
	if b.tokens > capacity {
		b.tokens = capacity
	}
	b.tokens -= float64(used)
	switch {
	case !b.suspended && b.tokens < 0:
		return 1
	case b.suspended && b.tokens >= float64(limit)*speedResumeSeconds:
		return -1
	}
	return 0
}

// loadSpeedLimits reads the per-client speed caps (bytes/sec) from the
// clients table; only rows with a positive cap are returned.
func loadSpeedLimits() map[string]int64 {
	db := database.GetDB()
	var rows []struct {
		Email      string
		LimitSpeed int64
	}
	if err := db.Model(&model.ClientRecord{}).
		Select("email, limit_speed").
		Where("limit_speed > 0 AND enable = ?", true).
		Scan(&rows).Error; err != nil {
		return nil
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Email] = r.LimitSpeed
	}
	return out
}

func loadLocalInbounds() []model.Inbound {
	db := database.GetDB()
	var inbounds []model.Inbound
	if err := db.Where("node_id IS NULL").Find(&inbounds).Error; err != nil {
		return nil
	}
	return inbounds
}

// resolveSpeedAPIPort mirrors CheckClientIpJob's resolution: running config,
// then the DB template, then the default.
func resolveSpeedAPIPort() int {
	if port, err := getAPIPortFromConfigPath(xray.GetConfigPath()); err == nil {
		return port
	}
	db := database.GetDB()
	var template model.Setting
	if err := db.Where("key = ?", "xrayTemplateConfig").First(&template).Error; err == nil {
		if port, err := getAPIPortFromConfigData([]byte(template.Value)); err == nil {
			return port
		}
	}
	return defaultXrayAPIPort
}

func speedProtocolSupported(protocol string) bool {
	switch protocol {
	case "vmess", "vless", "trojan", "shadowsocks", "hysteria", "masque":
		return true
	}
	return false
}

// suspendClient removes the user from every local inbound it is attached to
// and returns the tags it was actually removed from.
func suspendClient(api *xray.XrayAPI, inbounds []model.Inbound, email string) []string {
	var removed []string
	for i := range inbounds {
		inbound := &inbounds[i]
		if !speedProtocolSupported(string(inbound.Protocol)) {
			continue
		}
		clients, _ := service.ParseInboundSettingsClients(inbound.Settings)
		for _, c := range clients {
			if c.Email != email {
				continue
			}
			if c.Reverse != nil {
				// A reverse client's tunnel does not survive removal.
				continue
			}
			if err := api.RemoveUser(inbound.Tag, email); err != nil {
				logger.Warningf("[SpeedLimit] remove %s from %s failed: %v", email, inbound.Tag, err)
				continue
			}
			removed = append(removed, inbound.Tag)
		}
	}
	if len(removed) > 0 {
		logger.Infof("[SpeedLimit] suspended %s on %v (over speed limit)", email, removed)
	}
	return removed
}

// resumeClient re-adds the user to the given inbound tags (or every local
// inbound it belongs to when tags is empty).
func resumeClient(api *xray.XrayAPI, inbounds []model.Inbound, email string, tags []string) {
	tagSet := map[string]bool{}
	for _, t := range tags {
		tagSet[t] = true
	}
	for i := range inbounds {
		inbound := &inbounds[i]
		if len(tagSet) > 0 && !tagSet[inbound.Tag] {
			continue
		}
		protocol := string(inbound.Protocol)
		if !speedProtocolSupported(protocol) {
			continue
		}
		clients, _ := service.ParseInboundSettingsClients(inbound.Settings)
		for _, c := range clients {
			if c.Email != email || c.Reverse != nil {
				continue
			}
			clientBytes, _ := json.Marshal(c)
			var clientConfig map[string]any
			if err := json.Unmarshal(clientBytes, &clientConfig); err != nil {
				continue
			}
			if protocol == "shadowsocks" {
				var inboundSettings map[string]any
				if err := json.Unmarshal([]byte(inbound.Settings), &inboundSettings); err == nil {
					if method, ok := inboundSettings["method"].(string); ok && method != "" {
						clientConfig["cipher"] = method
					}
				}
			}
			if err := api.AddUser(protocol, inbound.Tag, clientConfig); err != nil {
				logger.Warningf("[SpeedLimit] re-add %s to %s failed: %v", email, inbound.Tag, err)
				continue
			}
			logger.Infof("[SpeedLimit] resumed %s on %s", email, inbound.Tag)
		}
	}
}
