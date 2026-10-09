package entclient

import (
	"context"
	"strings"
	"testing"

	"server/pkg/dbinit"
)

func TestOpenRejectsUnsafeDatabase(t *testing.T) {
	_, err := Open(context.Background(), Config{DB: dbinit.Config{
		Host: "127.0.0.1", User: "postgres", Database: "nova;drop",
	}})
	if err == nil || !strings.Contains(err.Error(), "invalid database name") {
		t.Fatalf("err = %v", err)
	}
}

func TestCloseNil(t *testing.T) {
	var c *Conn
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}
