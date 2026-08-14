package omnibus

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFileTypesUseCacheOnRerun drives two whole runs against a fake GitHub and
// counts the tree requests, which is the point of the cache.
func TestFileTypesUseCacheOnRerun(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	repos := []Repo{
		{Name: "sql-x-ray", FullName: "hihipy/sql-x-ray", HTMLURL: "https://github.com/hihipy/sql-x-ray",
			Size: 100, DefaultBranch: "main", PushedAt: "2026-08-01T00:00:00Z"},
		{Name: "tabs-2-json", FullName: "hihipy/tabs-2-json", HTMLURL: "https://github.com/hihipy/tabs-2-json",
			Size: 100, DefaultBranch: "main", PushedAt: "2026-08-01T00:00:00Z"},
	}

	treeCalls := 0
	remaining := 60
	mux := http.NewServeMux()
	limits := func(w http.ResponseWriter) {
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Hour).Unix()))
	}
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		limits(w)
		json.NewEncoder(w).Encode(map[string]any{"rate": map[string]any{
			"limit": 60, "remaining": remaining, "reset": time.Now().Add(time.Hour).Unix()}})
	})
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		remaining--
		limits(w)
		json.NewEncoder(w).Encode(repos)
	})
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		remaining--
		limits(w)
		if strings.Contains(r.URL.Path, "/git/trees/") {
			treeCalls++
			json.NewEncoder(w).Encode(map[string]any{"tree": []map[string]string{
				{"path": "scripts/a.sql", "type": "blob"},
				{"path": "scripts/b.sql", "type": "blob"},
				{"path": "README.md", "type": "blob"},
			}})
			return
		}
		w.Write(makeTarball(t, "w", map[string][]byte{"README.md": []byte("# x\n")}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "b.md")
	for pass := 1; pass <= 2; pass++ {
		if err := Run([]string{"-api", srv.URL, "-out", out, "hihipy"}, os.Stdout); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if treeCalls != 2 {
		t.Errorf("tree requests = %d across two runs, want 2 (the second run should be cached)", treeCalls)
	}
}

// fakeGitHubWithTrees serves the listing, the tree, and the tarball endpoints,
// decrementing a shared quota so budget behaviour can be exercised.
func fakeGitHubWithTrees(t *testing.T, repos []Repo, remaining *int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	limits := func(w http.ResponseWriter) {
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", *remaining))
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Hour).Unix()))
	}
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		limits(w)
		json.NewEncoder(w).Encode(map[string]any{"rate": map[string]any{
			"limit": 60, "remaining": *remaining, "reset": time.Now().Add(time.Hour).Unix()}})
	})
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		*remaining--
		limits(w)
		json.NewEncoder(w).Encode(repos)
	})
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		*remaining--
		limits(w)
		if strings.Contains(r.URL.Path, "/git/trees/") {
			json.NewEncoder(w).Encode(map[string]any{"tree": []map[string]string{
				{"path": "main.py", "type": "blob"},
			}})
			return
		}
		w.Write(makeTarball(t, "w", map[string][]byte{"README.md": []byte("# x\n")}))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}
