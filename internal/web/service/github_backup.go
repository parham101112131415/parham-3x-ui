package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

// GithubBackupService pushes the full-panel backup bundle to a GitHub repo
// and pulls it back for restore: ONE fixed file holding the main panel's
// database plus every node's database, replaced on every run, so the whole
// multi-location panel is rebuilt by importing that single file.
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

// BackupNow builds the full-panel backup bundle — this panel's database
// plus every registered node's database, pulled live from the nodes — and
// pushes it to the repo as ONE file, replacing the previous bundle. Used by
// the hourly job and the manual button. On PostgreSQL the historical
// single-file database backup is kept (bundles are a SQLite flow).
func (s *GithubBackupService) BackupNow(ctx context.Context) error {
	cfg, err := s.config()
	if err != nil {
		return err
	}
	if database.IsPostgres() {
		data, err := s.serverService.GetDb()
		if err != nil {
			return err
		}
		return s.push(ctx, cfg, data)
	}
	own, err := s.serverService.GetDb()
	if err != nil {
		return err
	}
	guid, _ := s.settingService.GetPanelGuid()
	nodes, err := s.allNodes()
	if err != nil {
		return err
	}
	entries := make([]BundleNodeData, 0, len(nodes))
	for _, n := range nodes {
		if !n.Enable || n.Guid == "" {
			continue
		}
		data, stale, ok := s.nodeDbForBackup(ctx, n)
		if !ok {
			logger.Warningf("GitHub backup: no database available for node %s (%s); recording it as missing in the bundle", n.Name, n.Guid)
			entries = append(entries, BundleNodeData{Guid: n.Guid, Name: n.Name, Missing: true})
			continue
		}
		entries = append(entries, BundleNodeData{Guid: n.Guid, Name: n.Name, Data: data, Stale: stale})
	}
	bundle, err := BuildBundle(own, guid, entries)
	if err != nil {
		return err
	}
	return s.push(ctx, cfg, bundle)
}

func (s *GithubBackupService) allNodes() ([]*model.Node, error) {
	db := database.GetDB()
	if db == nil {
		return nil, nil
	}
	var nodes []*model.Node
	if err := db.Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

func (s *GithubBackupService) remoteFor(n *model.Node) (*runtime.Remote, error) {
	if mgr := runtime.GetManager(); mgr != nil {
		return mgr.RemoteFor(n)
	}
	return runtime.NewRemote(n, nil), nil
}

// nodeDbCacheDir holds the last successfully pulled copy of each node's
// database, next to this panel's own database file.
func nodeDbCacheDir() string {
	return filepath.Join(filepath.Dir(config.GetDBPath()), "node-db-cache")
}

// nodeDbForBackup pulls the node's current database; on failure it falls
// back to the cached copy from the last successful pull (marked stale).
func (s *GithubBackupService) nodeDbForBackup(ctx context.Context, n *model.Node) (data []byte, stale bool, ok bool) {
	cachePath := filepath.Join(nodeDbCacheDir(), n.Guid+".db")
	if rt, err := s.remoteFor(n); err == nil {
		if fresh, err := rt.FetchDb(ctx); err == nil && len(fresh) > 0 {
			if err := os.MkdirAll(nodeDbCacheDir(), 0o755); err == nil {
				_ = os.WriteFile(cachePath, fresh, 0o644)
			}
			return fresh, false, true
		} else if err != nil {
			logger.Warningf("GitHub backup: pulling database of node %s failed: %v", n.Name, err)
		}
	}
	if cached, err := os.ReadFile(cachePath); err == nil && len(cached) > 0 {
		return cached, true, true
	}
	return nil, false, false
}

// pendingNodeRestoreDir holds per-node databases from a bundle import that
// could not be pushed to their node yet; the heartbeat job retries them.
func pendingNodeRestoreDir() string {
	return filepath.Join(filepath.Dir(config.GetDBPath()), "pending-node-restore")
}

type pendingNodeRestore struct {
	Guid string `json:"guid"`
	Name string `json:"name"`
}

func stashPendingNodeDb(guid, name string, data []byte) error {
	dir := pendingNodeRestoreDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	meta, _ := json.Marshal(pendingNodeRestore{Guid: guid, Name: name})
	if err := os.WriteFile(filepath.Join(dir, guid+".json"), meta, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, guid+".db"), data, 0o644)
}

// RestoreNodeDbsFromBundle pushes every node database in an imported bundle
// to its node (matched by guid, falling back to name, against the CURRENT —
// freshly restored — node table). Nodes that cannot be reached keep their
// database as a pending restore for the heartbeat job to retry.
func (s *GithubBackupService) RestoreNodeDbsFromBundle(ctx context.Context, b *ParsedBundle) {
	if len(b.NodeDbs) == 0 {
		return
	}
	nodes, err := s.allNodes()
	if err != nil {
		logger.Warning("bundle restore: cannot load nodes to distribute databases: ", err)
		return
	}
	byGuid := make(map[string]*model.Node, len(nodes))
	byName := make(map[string]*model.Node, len(nodes))
	for _, n := range nodes {
		if n.Guid != "" {
			byGuid[n.Guid] = n
		}
		byName[n.Name] = n
	}
	nameByGuid := make(map[string]string, len(b.Manifest.Nodes))
	for _, e := range b.Manifest.Nodes {
		nameByGuid[e.Guid] = e.Name
	}
	for guid, data := range b.NodeDbs {
		node := byGuid[guid]
		if node == nil {
			node = byName[nameByGuid[guid]]
		}
		if node == nil {
			logger.Warningf("bundle restore: no registered node matches %s (%s); keeping its database as a pending restore", nameByGuid[guid], guid)
			_ = stashPendingNodeDb(guid, nameByGuid[guid], data)
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 150*time.Second)
		err := s.pushDbToNode(pctx, node, data)
		cancel()
		if err != nil {
			logger.Warningf("bundle restore: pushing database to node %s failed (%v); keeping it as a pending restore", node.Name, err)
			_ = stashPendingNodeDb(guid, nameByGuid[guid], data)
			continue
		}
		logger.Infof("bundle restore: database of node %s restored", node.Name)
	}
}

func (s *GithubBackupService) pushDbToNode(ctx context.Context, n *model.Node, data []byte) error {
	rt, err := s.remoteFor(n)
	if err != nil {
		return err
	}
	return rt.RestoreDb(ctx, data)
}

var lastPendingPushUnix atomic.Int64

// PushPendingNodeRestores retries bundle databases that an earlier import
// could not deliver. Called from the node heartbeat job; self-throttled and
// a no-op when nothing is pending.
func (s *GithubBackupService) PushPendingNodeRestores(ctx context.Context) {
	now := time.Now().Unix()
	last := lastPendingPushUnix.Load()
	if now-last < 45 {
		return
	}
	if !lastPendingPushUnix.CompareAndSwap(last, now) {
		return
	}
	dir := pendingNodeRestoreDir()
	metas, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(metas) == 0 {
		return
	}
	nodes, err := s.allNodes()
	if err != nil {
		return
	}
	byGuid := make(map[string]*model.Node, len(nodes))
	byName := make(map[string]*model.Node, len(nodes))
	for _, n := range nodes {
		if n.Guid != "" {
			byGuid[n.Guid] = n
		}
		byName[n.Name] = n
	}
	for _, metaPath := range metas {
		raw, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta pendingNodeRestore
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		node := byGuid[meta.Guid]
		if node == nil {
			node = byName[meta.Name]
		}
		if node == nil || !node.Enable || node.Status != "online" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, meta.Guid+".db"))
		if err != nil || len(data) == 0 {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 150*time.Second)
		err = s.pushDbToNode(pctx, node, data)
		cancel()
		if err != nil {
			logger.Warningf("pending restore: pushing database to node %s still failing: %v", node.Name, err)
			continue
		}
		logger.Infof("pending restore: database of node %s restored", node.Name)
		_ = os.Remove(metaPath)
		_ = os.Remove(filepath.Join(dir, meta.Guid+".db"))
	}
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
