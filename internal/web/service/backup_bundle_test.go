package service

import (
	"bytes"
	"testing"
)

func TestBundleRoundTrip(t *testing.T) {
	master := []byte("master-db-bytes")
	nodes := []BundleNodeData{
		{Guid: "guid-a", Name: "us-east", Data: []byte("db-a")},
		{Guid: "guid-b", Name: "eu-west", Data: []byte("db-b"), Stale: true},
		{Guid: "guid-c", Name: "asia", Missing: true},
	}
	raw, err := BuildBundle(master, "master-guid", nodes)
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	if !IsBundle(raw) {
		t.Fatal("IsBundle false for a built bundle")
	}
	if IsBundle(master) {
		t.Fatal("IsBundle true for a raw database")
	}
	parsed, err := ParseBundle(raw)
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if !bytes.Equal(parsed.Master, master) {
		t.Fatal("master database did not round-trip")
	}
	if parsed.Manifest.MasterGuid != "master-guid" {
		t.Fatalf("manifest master guid = %q", parsed.Manifest.MasterGuid)
	}
	if len(parsed.Manifest.Nodes) != 3 {
		t.Fatalf("manifest nodes = %d", len(parsed.Manifest.Nodes))
	}
	if !bytes.Equal(parsed.NodeDbs["guid-a"], []byte("db-a")) {
		t.Fatal("node guid-a database did not round-trip")
	}
	if !bytes.Equal(parsed.NodeDbs["guid-b"], []byte("db-b")) {
		t.Fatal("node guid-b database did not round-trip")
	}
	if _, ok := parsed.NodeDbs["guid-c"]; ok {
		t.Fatal("missing node must not have a database entry")
	}
	byGuid := map[string]BundleNodeEntry{}
	for _, e := range parsed.Manifest.Nodes {
		byGuid[e.Guid] = e
	}
	if !byGuid["guid-b"].Stale {
		t.Fatal("guid-b should be marked stale")
	}
	if !byGuid["guid-c"].Missing || byGuid["guid-c"].File != "" {
		t.Fatal("guid-c should be missing with no file")
	}
	if byGuid["guid-a"].File != "nodes/guid-a.db" {
		t.Fatalf("guid-a file = %q", byGuid["guid-a"].File)
	}
}

func TestParseBundleRejectsGarbage(t *testing.T) {
	if _, err := ParseBundle([]byte("PK\x03\x04not-a-zip")); err == nil {
		t.Fatal("expected an error for a corrupt zip")
	}
}
