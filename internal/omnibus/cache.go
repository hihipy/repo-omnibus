package omnibus

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Fetching a repository's file list costs one request each, and the answer only
// changes when someone pushes. Keying the cache on the repository's last push
// means a rerun costs nothing until the repository actually changes, which is
// what makes the file-type column affordable inside a 60-per-hour budget.

type cacheEntry struct {
	PushedAt string   `json:"pushed_at"`
	Paths    []string `json:"paths"`
}

// TreeCache is a small JSON file under the user's cache directory.
type TreeCache struct {
	path    string
	entries map[string]cacheEntry
	dirty   bool
}

// OpenTreeCache reads the cache, returning an empty one when it is missing or
// unreadable. A cache that cannot be read is not an error worth stopping for.
func OpenTreeCache() *TreeCache {
	dir, err := os.UserCacheDir()
	if err != nil {
		return &TreeCache{entries: map[string]cacheEntry{}}
	}
	c := &TreeCache{
		path:    filepath.Join(dir, "repo-omnibus", "trees.json"),
		entries: map[string]cacheEntry{},
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return c
	}
	if err := json.Unmarshal(data, &c.entries); err != nil {
		c.entries = map[string]cacheEntry{}
	}
	return c
}

// Get returns the cached paths when the repository has not been pushed since.
func (c *TreeCache) Get(r Repo) ([]string, bool) {
	if c == nil {
		return nil, false
	}
	entry, ok := c.entries[r.FullName]
	if !ok || entry.PushedAt != r.PushedAt {
		return nil, false
	}
	return entry.Paths, true
}

func (c *TreeCache) Put(r Repo, paths []string) {
	if c == nil {
		return
	}
	c.entries[r.FullName] = cacheEntry{PushedAt: r.PushedAt, Paths: paths}
	c.dirty = true
}

// Save writes the cache back. A failure here costs a future request and nothing
// else, so it is reported to the caller rather than treated as fatal.
func (c *TreeCache) Save() error {
	if c == nil || !c.dirty || c.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(c.entries)
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0o644)
}

// Len reports how many repositories the cache holds, for the run's own report.
func (c *TreeCache) Len() int {
	if c == nil {
		return 0
	}
	return len(c.entries)
}
