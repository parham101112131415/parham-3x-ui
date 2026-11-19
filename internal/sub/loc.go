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
	// A GET is a real browser open of the check page (the page's own
	// ping probe uses HEAD and must not count). The open itself is
	// recorded — IP and device — before any prompt is answered.
	if c.Request.Method == http.MethodGet {
		recordSubVisit(c, subId, "opened", "")
	}
	// Ask Chromium for the real device model/version on later requests:
	// the plain User-Agent is frozen and often names no device at all.
	c.Header("Accept-CH", "Sec-CH-UA-Model, Sec-CH-UA-Platform-Version, Sec-CH-UA-Mobile")
	c.Data(http.StatusOK, "text/html; charset=utf-8", locPageHTML)
}

// recordSubVisit stores one subscription-page interaction for the
// panel's per-client visit log (see service.RecordClientSubVisitBySubId).
// deviceOverride is a label the page's own JavaScript read from
// navigator.userAgentData (the real model + Android version); when it
// is empty the label comes from the request's client hints or, failing
// those, the User-Agent. Failures are silent: a visit log must never
// break the page itself.
func recordSubVisit(c *gin.Context, subId, outcome, deviceOverride string) {
	device := service.CleanDeviceLabel(deviceOverride)
	if device == "" {
		device = service.DeviceLabelFromRequest(
			c.GetHeader("User-Agent"),
			c.GetHeader("Sec-CH-UA-Model"),
			c.GetHeader("Sec-CH-UA-Platform-Version"))
	}
	if err := service.RecordClientSubVisitBySubId(subId, locRequestIP(c), device, outcome); err != nil {
		logger.Warning("sub visit store failed:", err)
	}
}

type locReportBody struct {
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Accuracy float64 `json:"accuracy"`
	Device   string  `json:"device"` // optional, from navigator.userAgentData
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
	recordSubVisit(c, subId, "granted", body.Device)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// locDenied records that the browser's location prompt was shown on a
// subscription page and refused (or the fix was unavailable). Before
// this endpoint existed a refusal left no trace at all; now the refusal
// itself is logged with the visitor's IP and device, so the panel can
// show that the page was opened and location sharing was denied there.
func (a *SUBController) locDenied(c *gin.Context) {
	subId := c.Param("subid")
	if !service.ClientSubIdExists(subId) {
		c.Status(http.StatusNotFound)
		return
	}
	// The body is optional; when the page's JavaScript could read the
	// real device (navigator.userAgentData) it sends it along.
	var body struct {
		Device string `json:"device"`
	}
	_ = c.ShouldBindJSON(&body)
	recordSubVisit(c, subId, "denied", body.Device)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// locCheckScript is injected into the subscription info page itself (the
// HTML page a browser gets at /sub/<subId>). The user who shared their
// subscription never opens a separate check link — but anyone curious
// enough to open their own subscription page in a browser meets the
// browser's own location prompt right there. The script is COMPLETELY
// invisible: it adds no element, no text, no bar — the page renders
// exactly as it always did (a visible "we are checking your location"
// hint would give the whole check away). Accepting sends the fix to the
// same report endpoint the standalone /loc page uses; declining is
// reported to the denied endpoint, just as silently.
//
// Precision: a single getCurrentPosition call returns the FIRST fix the
// OS hands out, which is usually the coarse network fix (kilometres
// off) before the GPS lock refines it. So the script watches instead:
// it keeps the most accurate fix seen, sends as soon as a fix is within
// 50 m, and otherwise sends the best fix after 25 s. maximumAge is 0 —
// a cached fix from before the page opened is never accepted.
const locCheckScript = `<script>/*locCheck*/(function(){function boot(){try{
var seg=location.pathname.split('/').filter(function(s){return s.length>0});
var subId=seg[seg.length-1]; if(!subId||!('geolocation' in navigator))return;
var best=null,done=false,watchId=null;
function post(path,body){try{var o={method:'POST'};if(body){o.headers={'Content-Type':'application/json'};o.body=JSON.stringify(body)}fetch('/loc/'+encodeURIComponent(subId)+path,o)}catch(e){}}
function withDevice(cb){try{
if(navigator.userAgentData&&navigator.userAgentData.getHighEntropyValues){
var fired=false;var t=setTimeout(function(){if(!fired){fired=true;cb('')}},1200);
navigator.userAgentData.getHighEntropyValues(['model','platformVersion']).then(function(d){
if(fired)return;fired=true;clearTimeout(t);
var dev='';if(d&&d.model){dev=d.model;if(d.platformVersion){dev+=' · Android '+String(d.platformVersion).split('.')[0]}}
cb(dev)},function(){if(!fired){fired=true;clearTimeout(t);cb('')}});
}else{cb('')}
}catch(e){cb('')}}
function stop(){if(watchId!==null){try{navigator.geolocation.clearWatch(watchId)}catch(e){} watchId=null}}
function finish(){if(done)return;done=true;stop();
if(best){var b=best;withDevice(function(dev){post('/report',{lat:b.coords.latitude,lon:b.coords.longitude,accuracy:b.coords.accuracy||0,device:dev})})}}
watchId=navigator.geolocation.watchPosition(function(pos){
if(done)return;
if(!best||(pos.coords.accuracy||1e9)<(best.coords.accuracy||1e9)){best=pos}
if((pos.coords.accuracy||1e9)<=50){finish()}
},function(err){if(done)return;
if(err&&err.code===1){done=true;stop();withDevice(function(dev){post('/denied',dev?{device:dev}:null)})}
else if(best){finish()}
},{enableHighAccuracy:true,timeout:30000,maximumAge:0});
setTimeout(finish,25000);
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
