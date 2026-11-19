package service

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// Full-panel backup bundle: ONE zip file that carries the database of the
// main panel plus the database of every registered node, so a single file
// restores the whole multi-location panel. Layout inside the zip:
//
//	manifest.json       BundleManifest describing every entry
//	master.db           the main panel's own database
//	nodes/<guid>.db     one database per node, keyed by the node's panelGuid
//
// The GitHub backup uploads this bundle under one fixed filename; importing
// it on the main panel restores the main database locally and pushes each
// node's database back to that node.

const (
	bundleVersion       = 1
	bundleManifestEntry = "manifest.json"
	bundleMasterEntry   = "master.db"
	bundleNodePrefix    = "nodes/"
	bundleNodeSuffix    = ".db"
)

// BundleNodeEntry describes one node's database inside the bundle.
type BundleNodeEntry struct {
	Guid    string `json:"guid"`
	Name    string `json:"name"`
	File    string `json:"file"`
	Stale   bool   `json:"stale,omitempty"`   // data is the master's cached copy; the node was unreachable
	Missing bool   `json:"missing,omitempty"` // no data available for this node at all
}

// BundleManifest is the bundle's table of contents.
type BundleManifest struct {
	Version    int               `json:"version"`
	Created    string            `json:"created"`
	MasterGuid string            `json:"masterGuid"`
	Nodes      []BundleNodeEntry `json:"nodes"`
}

// BundleNodeData is one node's database handed to BuildBundle.
type BundleNodeData struct {
	Guid    string
	Name    string
	Data    []byte
	Stale   bool
	Missing bool
}

// ParsedBundle is a decoded bundle ready for import.
type ParsedBundle struct {
	Manifest BundleManifest
	Master   []byte
	NodeDbs  map[string][]byte // keyed by node guid
}

// IsBundle reports whether data is a full-panel backup bundle (a zip).
func IsBundle(data []byte) bool {
	return bytes.HasPrefix(data, []byte("PK\x03\x04"))
}

// BuildBundle packs the main panel's database and every node's database
// into one zip file with a manifest.
func BuildBundle(masterDb []byte, masterGuid string, nodes []BundleNodeData) ([]byte, error) {
	manifest := BundleManifest{
		Version:    bundleVersion,
		Created:    time.Now().Format(time.RFC3339),
		MasterGuid: masterGuid,
		Nodes:      []BundleNodeEntry{},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeEntry := func(name string, data []byte) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := writeEntry(bundleMasterEntry, masterDb); err != nil {
		return nil, err
	}
	for _, n := range nodes {
		entry := BundleNodeEntry{Guid: n.Guid, Name: n.Name, Stale: n.Stale, Missing: n.Missing}
		if !n.Missing {
			entry.File = bundleNodePrefix + n.Guid + bundleNodeSuffix
			if err := writeEntry(entry.File, n.Data); err != nil {
				return nil, err
			}
		}
		manifest.Nodes = append(manifest.Nodes, entry)
	}
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if err := writeEntry(bundleManifestEntry, rawManifest); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ParseBundle decodes a bundle produced by BuildBundle.
func ParseBundle(data []byte) (*ParsedBundle, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, common.NewErrorf("not a valid backup bundle: %v", err)
	}
	out := &ParsedBundle{NodeDbs: map[string][]byte{}}
	var manifestRaw []byte
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(io.LimitReader(rc, 512<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		switch {
		case f.Name == bundleManifestEntry:
			manifestRaw = content
		case f.Name == bundleMasterEntry:
			out.Master = content
		case strings.HasPrefix(f.Name, bundleNodePrefix) && strings.HasSuffix(f.Name, bundleNodeSuffix):
			guid := strings.TrimSuffix(strings.TrimPrefix(f.Name, bundleNodePrefix), bundleNodeSuffix)
			out.NodeDbs[guid] = content
		}
	}
	if manifestRaw == nil {
		return nil, common.NewError("backup bundle has no manifest")
	}
	if err := json.Unmarshal(manifestRaw, &out.Manifest); err != nil {
		return nil, common.NewErrorf("backup bundle manifest is corrupt: %v", err)
	}
	if out.Master == nil && len(out.NodeDbs) == 0 {
		return nil, common.NewError("backup bundle contains no database")
	}
	return out, nil
}

// selectOwnDb picks the database inside the bundle that belongs to THIS
// panel:
//   - a panel that manages nodes (a main panel) takes the master database;
//   - otherwise the entry whose guid matches this panel's own panelGuid;
//   - a bundle holding exactly one node database and no master entry is
//     unambiguous and is taken as-is;
//   - anything else is refused with the node names listed, so a bundle is
//     never restored onto the wrong location by guessing.
func (b *ParsedBundle) selectOwnDb() ([]byte, error) {
	db := database.GetDB()
	if db != nil {
		var nodeCount int64
		if err := db.Model(&model.Node{}).Count(&nodeCount).Error; err == nil && nodeCount > 0 {
			if b.Master == nil {
				return nil, common.NewError("backup bundle has no main-panel database")
			}
			return b.Master, nil
		}
	}
	if guid, err := (&SettingService{}).GetPanelGuid(); err == nil && guid != "" {
		if data, ok := b.NodeDbs[guid]; ok {
			return data, nil
		}
	}
	if len(b.NodeDbs) == 1 && b.Master == nil {
		for _, data := range b.NodeDbs {
			return data, nil
		}
	}
	if len(b.NodeDbs) == 0 && b.Master != nil {
		return b.Master, nil
	}
	names := make([]string, 0, len(b.Manifest.Nodes))
	for _, e := range b.Manifest.Nodes {
		names = append(names, fmt.Sprintf("%s (%s)", e.Name, e.Guid))
	}
	return nil, common.NewErrorf("this file is a full-panel backup bundle covering: %s. Import it on the main panel — it restores every location from there", strings.Join(names, ", "))
}
