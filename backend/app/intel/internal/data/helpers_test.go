package data_test

import (
	"testing"

	"server/pkg/events"
)

func mustEnvelope(t *testing.T, subject string) events.Envelope {
	t.Helper()
	env, err := events.New("intel", subject, "", map[string]int{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	return env
}
