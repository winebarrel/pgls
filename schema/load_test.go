package schema

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "users.sql"), `CREATE TABLE users (id bigint, name text);`)
	mustWrite(t, filepath.Join(dir, "sub", "orders.sql"), `CREATE TABLE orders (id bigint);`)
	mustWrite(t, filepath.Join(dir, "readme.md"), `not sql`)

	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Tables["users"]; !ok {
		t.Error("users missing")
	}
	if _, ok := s.Tables["orders"]; !ok {
		t.Error("orders missing (recursive walk failed)")
	}
	if len(s.Tables) != 2 {
		t.Errorf("want 2 tables, got %d", len(s.Tables))
	}
}

func TestLoadParseError(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "broken.sql"), `CREATE TABLE (`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
