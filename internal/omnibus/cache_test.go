package omnibus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tempCache points the cache at a throwaway directory, so a test never touches
// the real one.
func tempCache(t *testing.T) *TreeCache {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir()) // macOS reads HOME rather than XDG
	return OpenTreeCache()
}

func TestCacheRoundTrip(t *testing.T) {
	c := tempCache(t)
	r := Repo{FullName: "hihipy/sql-x-ray", PushedAt: "2026-08-01T00:00:00Z"}
	paths := []string{"scripts/postgres-xray.sql", "README.md"}

	if _, ok := c.Get(r); ok {
		t.Fatal("an empty cache reported a hit")
	}
	c.Put(r, paths)
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reopened := OpenTreeCache()
	got, ok := reopened.Get(r)
	if !ok {
		t.Fatal("the saved entry was not found on reopening")
	}
	if strings.Join(got, ",") != strings.Join(paths, ",") {
		t.Errorf("paths = %v, want %v", got, paths)
	}
}

func TestCacheMissesAfterAPush(t *testing.T) {
	c := tempCache(t)
	r := Repo{FullName: "hihipy/sql-x-ray", PushedAt: "2026-08-01T00:00:00Z"}
	c.Put(r, []string{"old.sql"})

	pushed := r
	pushed.PushedAt = "2026-08-14T00:00:00Z"
	if _, ok := c.Get(pushed); ok {
		t.Error("a repository pushed since caching should miss")
	}
	if _, ok := c.Get(r); !ok {
		t.Error("the unchanged repository should still hit")
	}
}

func TestCacheSurvivesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("HOME", dir)

	base, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no cache directory on this platform")
	}
	path := filepath.Join(base, "repo-omnibus", "trees.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := OpenTreeCache()
	if c.Len() != 0 {
		t.Errorf("a corrupt cache should read as empty, got %d entries", c.Len())
	}
	c.Put(Repo{FullName: "a/b", PushedAt: "x"}, []string{"a.py"})
	if err := c.Save(); err != nil {
		t.Errorf("a corrupt cache should still be writable: %v", err)
	}
}

func TestNilCacheIsSafe(t *testing.T) {
	var c *TreeCache
	if _, ok := c.Get(Repo{}); ok {
		t.Error("a nil cache reported a hit")
	}
	c.Put(Repo{}, nil)
	if err := c.Save(); err != nil {
		t.Errorf("Save on a nil cache: %v", err)
	}
	if c.Len() != 0 {
		t.Error("a nil cache should be empty")
	}
}
