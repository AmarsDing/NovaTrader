package biz

import (
	"context"
	"errors"
	"testing"
)

func TestResearchDisabledBeforeAccessingPorts(t *testing.T) {
	uc := NewResearchUsecase(nil, nil, nil, ResearchSettings{})
	if _, err := uc.CreateResearch(context.Background(), ResearchRequest{}, "test"); !errors.Is(err, ErrConflict) {
		t.Fatal("default off was not enforced", err)
	}
}
func TestResearchJSONBoundary(t *testing.T) {
	for _, input := range []string{"[]", "null", `{} {}`, `{"nested":[{"account_id":"private"}]}`, `{"api_key":"private"}`, `{"credential":"private"}`} {
		if _, err := validateResearchJSON(input); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("unsafe JSON accepted: %s (%v)", input, err)
		}
	}
	value, err := validateResearchJSON(`{"kind":"account_nav","notional":9007199254740993}`)
	if err != nil || value["notional"].(interface{ String() string }).String() != "9007199254740993" {
		t.Fatal("exact input number lost", value, err)
	}
}
