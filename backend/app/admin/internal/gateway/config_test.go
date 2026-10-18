package gateway

import (
	"encoding/json"
	"server/pkg/params"
	"strings"
	"testing"
)

func TestConfigProjectionAndPreviewNeverExposeSecret(t *testing.T) {
	s := params.Snapshot{Revision: 8, Values: map[string]string{"app.brain.large.key": "old-secret", "m06.select_score": "75"}}
	preview := previewConfig(s, map[string]string{"app.brain.large.key": "new-secret", "m06.select_score": "80"}, false)
	raw, _ := json.Marshal(preview)
	if strings.Contains(string(raw), "old-secret") || strings.Contains(string(raw), "new-secret") {
		t.Fatal("preview leaked secret")
	}
	raw, _ = json.Marshal(configPublic(s))
	if strings.Contains(string(raw), "old-secret") {
		t.Fatal("read leaked secret")
	}
	if len(preview.UnknownEffects) == 0 || !preview.InvalidatesPlans || len(preview.Changes) != 2 {
		t.Fatalf("incomplete effects %+v", preview)
	}
}
