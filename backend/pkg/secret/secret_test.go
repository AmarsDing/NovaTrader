package secret

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	sealed, err := Encrypt(key, []byte("tushare-token"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Decrypt(key, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "tushare-token" {
		t.Fatal(string(plain))
	}
	key[0] = 8
	if _, err := Decrypt(key, sealed); err == nil {
		t.Fatal("wrong key should fail")
	}
}

func TestLoadKeyCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	key, err := LoadKey(path)
	if err != nil || len(key) != 32 {
		t.Fatal(err, len(key))
	}
	again, err := LoadKey(path)
	if err != nil || string(again) != string(key) {
		t.Fatal("key changed")
	}
}
