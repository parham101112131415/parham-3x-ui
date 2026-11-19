package sub

import (
	_ "embed"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// loc.go serves the /loc/<subId> connection-check page. When a subscriber
// opens it in a browser and accepts the browser's own location prompt,
// the page sends the device's geolocation fix back to the report
// endpoint, where it is stored against the subscription's client (see
// service.RecordClientGpsBySubId). This is the only channel through
// which a true device location can reach the panel: the VPN protocols
// themselves carry no location data at all, and a subscriber who never
// opens the page (or declines the prompt) simply never reports one.

//go:embed loc_page.html
var locPageHTML []byte

func (a *SUBController) locPage(c *gin.Context) {
	subId := c.Param("subid")
	if !service.ClientSubIdExists(subId) {
		c.Status(http.StatusNotFound)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", locPageHTML)
}

type locReportBody struct {
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Accuracy float64 `json:"accuracy"`
}

func (a *SUBController) locReport(c *gin.Context) {
	subId := c.Param("subid")
	if !service.ClientSubIdExists(subId) {
		c.Status(http.StatusNotFound)
		return
	}
	var body locReportBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false})
		return
	}
	if body.Lat < -90 || body.Lat > 90 || body.Lon < -180 || body.Lon > 180 ||
		(body.Lat == 0 && body.Lon == 0) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false})
		return
	}
	if err := service.RecordClientGpsBySubId(subId, body.Lat, body.Lon, body.Accuracy, locRequestIP(c)); err != nil {
		logger.Warning("loc report store failed:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// locRequestIP is the report's source address: the first X-Forwarded-For
// hop when a proxy chain is in front (the panel's own nginx sets it),
// otherwise the direct peer.
func locRequestIP(c *gin.Context) string {
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	if host, _, err := net.SplitHostPort(c.Request.RemoteAddr); err == nil {
		return host
	}
	return c.Request.RemoteAddr
}
