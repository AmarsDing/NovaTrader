package service

import (
	"encoding/json"
	"os"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	v1 "server/api/backtest/v1"
	"server/pkg/dataset"
)

func TestDatasetExportPreservesExactArtifact(t *testing.T) {
	p, e := os.ReadFile("../../../../pkg/dataset/testdata/manifest_v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var m dataset.Manifest
	if e = json.Unmarshal(p, &m); e != nil {
		t.Fatal(e)
	}
	reply, e := datasetReply(&m)
	if e != nil {
		t.Fatal(e)
	}
	wire, e := protojson.Marshal(reply)
	if e != nil {
		t.Fatal(e)
	}
	var decoded v1.DatasetReply
	if e = protojson.Unmarshal(wire, &decoded); e != nil {
		t.Fatal(e)
	}
	var exported dataset.Manifest
	if e = json.Unmarshal([]byte(decoded.ManifestJson), &exported); e != nil {
		t.Fatal(e)
	}
	if e = exported.Verify(); e != nil {
		t.Fatalf("HTTP export changed artifact hash/numeric precision: %v", e)
	}
	if exported.Hash != m.Hash || decoded.Hash != m.Hash {
		t.Fatal("export hash changed")
	}
}
