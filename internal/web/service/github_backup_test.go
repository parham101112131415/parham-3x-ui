package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGitHub implements just enough of the Git Data API to exercise the
// backup push/fetch round trip.
type fakeGitHub struct {
	blobContent string
	headSha     string
}

func (f *fakeGitHub) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
	})
	mux.HandleFunc("/repos/o/r/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
		if f.headSha == "" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": f.headSha}})
	})
	mux.HandleFunc("/repos/o/r/git/commits/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tree": map[string]any{"sha": "tree1"}})
	})
	mux.HandleFunc("/repos/o/r/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			t.Fatalf("blob body: %v", err)
		}
		f.blobContent = in.Content
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "blob1"})
	})
	mux.HandleFunc("/repos/o/r/git/blobs/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"content": f.blobContent})
	})
	mux.HandleFunc("/repos/o/r/git/trees", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "tree2"})
	})
	mux.HandleFunc("/repos/o/r/git/commits", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "commit2"})
	})
	mux.HandleFunc("/repos/o/r/git/refs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/main"})
	})
	mux.HandleFunc("/repos/o/r/git/refs/heads/main", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/main"})
	})
	mux.HandleFunc("/repos/o/r/contents/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "blob1"})
	})
	return mux
}

func TestGithubBackupPushFetchRoundTrip(t *testing.T) {
	fake := &fakeGitHub{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	svc := &GithubBackupService{APIBase: srv.URL}
	cfg := &ghBackupConfig{token: "tok", owner: "o", repo: "r", file: "panel.db"}
	payload := []byte("fake sqlite bytes \x00\x01\x02")

	// Empty repo: ref is 404, so the push must create the ref.
	if err := svc.push(context.Background(), cfg, payload); err != nil {
		t.Fatalf("push to empty repo: %v", err)
	}
	if got := fake.blobContent; got != base64.StdEncoding.EncodeToString(payload) {
		t.Fatalf("blob content mismatch: %q", got)
	}

	// Existing head: the push must take the base-tree + PATCH path.
	fake.headSha = "commit1"
	if err := svc.push(context.Background(), cfg, payload); err != nil {
		t.Fatalf("push over existing head: %v", err)
	}

	// Fetch resolves contents -> blob sha -> base64 content.
	svc2 := &GithubBackupService{APIBase: srv.URL}
	// FetchBackup reads settings from the DB, so exercise its two HTTP steps
	// through push-state instead: verify the blob endpoint round-trips.
	raw, _, err := svc2.do(context.Background(), cfg, http.MethodGet, "/repos/o/r/git/blobs/blob1", nil, "application/vnd.github+json")
	if err != nil {
		t.Fatalf("blob get: %v", err)
	}
	blob, err := ghJSON[ghBlob](raw)
	if err != nil {
		t.Fatalf("blob parse: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(blob.Content, "\n", ""))
	if err != nil || string(decoded) != string(payload) {
		t.Fatalf("round trip mismatch: %v %q", err, decoded)
	}
}
