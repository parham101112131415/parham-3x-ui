package job

import (
	"context"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// GithubBackupJob pushes the full-panel backup bundle to the configured
// GitHub repo once an hour when the GitHub backup is enabled: ONE fixed
// file holding every location's database, replaced on every run.
type GithubBackupJob struct {
	settingService service.SettingService
	backupService  service.GithubBackupService
}

// NewGithubBackupJob creates a new GitHub backup job instance.
func NewGithubBackupJob() *GithubBackupJob {
	return new(GithubBackupJob)
}

// Run executes one backup when enabled and a token is configured.
func (j *GithubBackupJob) Run() {
	enabled, err := j.settingService.GetGhBackupEnable()
	if err != nil || !enabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := j.backupService.BackupNow(ctx); err != nil {
		logger.Warning("GithubBackupJob: backup failed: ", err)
	}
}
