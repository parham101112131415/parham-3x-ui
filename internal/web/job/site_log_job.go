package job

import (
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// SiteLogJob folds the local Xray access log into per-client destination
// statistics (which hosts each client connected to, and how often). It
// only records this panel's own observations; the node traffic sync job
// merges the other panels' reports.
type SiteLogJob struct {
	inboundService *service.InboundService
}

// NewSiteLogJob creates a new site-log collector job instance.
func NewSiteLogJob() *SiteLogJob {
	return &SiteLogJob{inboundService: &service.InboundService{}}
}

func (j *SiteLogJob) Run() {
	j.inboundService.CollectClientSites()
}
