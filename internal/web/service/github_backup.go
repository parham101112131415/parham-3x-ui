package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// GithubBackupService pushes this panel's database backup to a GitHub repo
// and pulls it back for restore. It mirrors the old panel's GitHub backup:
// one fixed file per panel, replaced on every run, so a dead host can be
// rebuilt by importing that file on a fresh panel.
//
// Writes go through the Git Data API (blob -> tree -> commit -> ref) rather
// than the Contents API so backups larger than 1 MB work unchanged.
type GithubBackupService struct {
	settingService SettingService
	serverService  ServerService
	APIBase        string // override in tests; empty means https://api.github.com
	Client         *http.Client
}

func (s *GithubBackupService) apiBase() string {
	if s.APIBase != "" {
		return strings.TrimSuffix(s.APIBase, "/")
	}
	return "https://api.github.com"
}

func (s *GithubBackupService) httpClient() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 120 * time.Second}
}

type ghBackupConfig struct {
	token string
	owner string
	repo  string
	file  string
}

// config resolves the GitHub backup settings. A token and a repo in
// owner/name form are required; the file falls back to this panel's stable
// backup filename.
func (s *GithubBackupService) config() (*ghBackupConfig, error) {
	token, err := s.settingService.GetGhBackupToken()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, common.NewError("GitHub backup token is not set")
	}
	repo, err := s.settingService.GetGhBackupRepo()
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.Trim(strings.TrimSpace(repo), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, common.NewError("GitHub backup repo must be in owner/name form")
	}
	file, err := s.settingService.GetGhBackupFile()
	if err != nil {
		return nil, err
	}
	file = strings.TrimSpace(file)
	if file == "" {
		file = s.serverService.StableBackupFilename()
	}
	return &ghBackupConfig{token: token, owner: parts[0], repo: parts[1], file: file}, nil
}

// Filename returns the repo path this panel backs up to.
func (s *GithubBackupService) Filename() (string, error) {
	cfg, err := s.config()
	if err != nil {
		return "", err
	}
	return cfg.file, nil
}

func (s *GithubBackupService) do(ctx context.Context, cfg *ghBackupConfig, method, path string, body any, accept string) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.apiBase()+path, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.token)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(data))
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return nil, resp.StatusCode, common.NewErrorf("GitHub API %s %s: HTTP %d: %s", method, path, resp.StatusCode, snippet)
	}
	return data, resp.StatusCode, nil
}

func ghJSON[T any](data []byte) (*T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

type ghRepoInfo struct {
	DefaultBranch string `json:"default_branch"`
}

type ghRef struct {
	Object struct {
		Sha string `json:"sha"`
	} `json:"object"`
}

type ghCommit struct {
	Tree struct {
		Sha string `json:"sha"`
	} `json:"tree"`
}

type ghSha struct {
	Sha string `json:"sha"`
}

type ghBlob struct {
	Content string `json:"content"`
}

// BackupNow exports the database and pushes it to the repo, replacing the
// panel's fixed backup file. Used by the hourly job and the manual button.
func (s *GithubBackupService) BackupNow(ctx context.Context) error {
	cfg, err := s.config()
	if err != nil {
		return err
	}
	data, err := s.serverService.GetDb()
	if err != nil {
		return err
	}
	return s.push(ctx, cfg, data)
}

func (s *GithubBackupService) push(ctx context.Context, cfg *ghBackupConfig, data []byte) error {
	repoPath := fmt.Sprintf("/repos/%s/%s", cfg.owner, cfg.repo)
	raw, _, err := s.do(ctx, cfg, http.MethodGet, repoPath, nil, "application/vnd.github+json")
	if err != nil {
		return err
	}
	repoInfo, err := ghJSON[ghRepoInfo](raw)
	if err != nil {
		return err
	}
	branch := repoInfo.DefaultBranch
	if branch == "" {
		branch = "main"
	}

	// Current head commit + its tree (absent on an empty repo).
	var headSha, baseTree string
	raw, status, err := s.do(ctx, cfg, http.MethodGet, repoPath+"/git/ref/heads/"+branch, nil, "application/vnd.github+json")
	if err != nil && status != http.StatusNotFound {
		return err
	}
	if err == nil {
		ref, err := ghJSON[ghRef](raw)
		if err != nil {
			return err
		}
		headSha = ref.Object.Sha
		raw, _, err = s.do(ctx, cfg, http.MethodGet, repoPath+"/git/commits/"+headSha, nil, "application/vnd.github+json")
		if err != nil {
			return err
		}
		commit, err := ghJSON[ghCommit](raw)
		if err != nil {
			return err
		}
		baseTree = commit.Tree.Sha
	}

	raw, _, err = s.do(ctx, cfg, http.MethodPost, repoPath+"/git/blobs", map[string]any{
		"content":  base64.StdEncoding.EncodeToString(data),
		"encoding": "base64",
	}, "application/vnd.github+json")
	if err != nil {
		return err
	}
	blob, err := ghJSON[ghSha](raw)
	if err != nil {
		return err
	}

	treeBody := map[string]any{
		"tree": []map[string]any{{
			"path": cfg.file,
			"mode": "100644",
			"type": "blob",
			"sha":  blob.Sha,
		}},
	}
	if baseTree != "" {
		treeBody["base_tree"] = baseTree
	}
	raw, _, err = s.do(ctx, cfg, http.MethodPost, repoPath+"/git/trees", treeBody, "application/vnd.github+json")
	if err != nil {
		return err
	}
	tree, err := ghJSON[ghSha](raw)
	if err != nil {
		return err
	}

	commitBody := map[string]any{
		"message": fmt.Sprintf("parham panel backup %s (%s)", time.Now().Format("2006-01-02 15:04"), cfg.file),
		"tree":    tree.Sha,
	}
	if headSha != "" {
		commitBody["parents"] = []string{headSha}
	}
	raw, _, err = s.do(ctx, cfg, http.MethodPost, repoPath+"/git/commits", commitBody, "application/vnd.github+json")
	if err != nil {
		return err
	}
	newCommit, err := ghJSON[ghSha](raw)
	if err != nil {
		return err
	}

	if headSha == "" {
		_, _, err = s.do(ctx, cfg, http.MethodPost, repoPath+"/git/refs", map[string]any{
			"ref": "refs/heads/" + branch,
			"sha": newCommit.Sha,
		}, "application/vnd.github+json")
		return err
	}
	_, _, err = s.do(ctx, cfg, http.MethodPatch, repoPath+"/git/refs/heads/"+branch, map[string]any{
		"sha": newCommit.Sha,
	}, "application/vnd.github+json")
	return err
}

// FetchBackup downloads this panel's backup file from the repo.
func (s *GithubBackupService) FetchBackup(ctx context.Context) ([]byte, error) {
	cfg, err := s.config()
	if err != nil {
		return nil, err
	}
	repoPath := fmt.Sprintf("/repos/%s/%s", cfg.owner, cfg.repo)
	raw, _, err := s.do(ctx, cfg, http.MethodGet, repoPath+"/contents/"+cfg.file, nil, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	meta, err := ghJSON[ghSha](raw)
	if err != nil {
		return nil, err
	}
	raw, _, err = s.do(ctx, cfg, http.MethodGet, repoPath+"/git/blobs/"+meta.Sha, nil, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	blob, err := ghJSON[ghBlob](raw)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(blob.Content, "\n", ""))
}

// bytesFile adapts a bytes.Reader to multipart.File for ServerService.ImportDB.
type bytesFile struct {
	*bytes.Reader
}

func (bytesFile) Close() error { return nil }

// ImportNow downloads the backup from the repo and restores it, keeping this
// machine's host settings (the same default as the file-import dialog). The
// caller restarts the panel afterwards, mirroring the importDB handler.
func (s *GithubBackupService) ImportNow(ctx context.Context) error {
	data, err := s.FetchBackup(ctx)
	if err != nil {
		return err
	}
	return s.serverService.ImportDB(bytesFile{bytes.NewReader(data)}, true)
}
