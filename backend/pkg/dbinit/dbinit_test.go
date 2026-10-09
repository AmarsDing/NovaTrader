package dbinit

import "testing"

func TestSplitAdminDSN(t *testing.T) {
	name, admin, err := splitAdminDSN(Config{
		DSN: "postgres://postgres:secret@127.0.0.1:5432/novatrader?sslmode=disable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if name != "novatrader" {
		t.Fatalf("database = %q", name)
	}
	if admin != "postgres://postgres:secret@127.0.0.1:5432/postgres?sslmode=disable" {
		t.Fatalf("admin dsn = %q", admin)
	}
}

func TestSplitAdminDSNFromFields(t *testing.T) {
	name, admin, err := splitAdminDSN(Config{
		Host: "127.0.0.1", Port: 5432, User: "postgres", Password: "postgres", Database: "novatrader", SSLMode: "disable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if name != "novatrader" {
		t.Fatalf("database = %q", name)
	}
	if admin == "" || name == "postgres" {
		t.Fatalf("unexpected admin dsn %q", admin)
	}
}

func TestRejectsUnsafeExtension(t *testing.T) {
	err := EnsureExtension(t.Context(), Config{Database: "novatrader", Host: "127.0.0.1", User: "postgres"}, "vector;drop")
	if err == nil {
		t.Fatal("expected invalid extension name")
	}
}

func TestRejectsUnsafeDatabaseName(t *testing.T) {
	if err := EnsureDatabase(t.Context(), Config{Database: "nova;drop", Host: "127.0.0.1", User: "postgres"}); err == nil {
		t.Fatal("expected invalid database name")
	}
}
