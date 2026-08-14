package omnibus

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGitHub serves the two endpoints the client uses, with a quota that
// decrements on every request.
func fakeGitHub(t *testing.T, repos []Repo, remaining *int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	writeLimits := func(w http.ResponseWriter) {
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", *remaining))
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Hour).Unix()))
	}

	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		writeLimits(w)
		json.NewEncoder(w).Encode(map[string]any{
			"rate": map[string]any{
				"limit": 60, "remaining": *remaining,
				"reset": time.Now().Add(time.Hour).Unix(),
			},
		})
	})

	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		*remaining--
		writeLimits(w)
		json.NewEncoder(w).Encode(repos)
	})

	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		if *remaining <= 0 {
			writeLimits(w)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		*remaining--
		writeLimits(w)
		w.Write(makeTarball(t, "w", map[string][]byte{
			"README.md": []byte("# repo\n"),
			"main.go":   []byte("package main\n"),
		}))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func testRepos(n int) []Repo {
	out := make([]Repo, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, Repo{
			Name: fmt.Sprintf("r%d", i), FullName: fmt.Sprintf("hihipy/r%d", i),
			HTMLURL: fmt.Sprintf("https://github.com/hihipy/r%d", i),
			Size:    100, DefaultBranch: "main",
		})
	}
	return out
}

func TestRunWritesBundle(t *testing.T) {
	remaining := 60
	srv := fakeGitHub(t, testRepos(3), &remaining)
	out := filepath.Join(t.TempDir(), "bundle.md")

	err := Run([]string{"-all", "-api", srv.URL, "-out", out, "hihipy"}, os.Stdout)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	doc, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# r1", "# r2", "# r3", "3 repositories"} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("bundle missing %q", want)
		}
	}
}

func TestRunRefusesWhenQuotaTooSmall(t *testing.T) {
	remaining := 4 // one is spent listing, leaving 3 for 6 repositories
	srv := fakeGitHub(t, testRepos(6), &remaining)
	out := filepath.Join(t.TempDir(), "bundle.md")

	err := Run([]string{"-all", "-api", srv.URL, "-out", out, "hihipy"}, os.Stdout)
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "requests needed") {
		t.Errorf("error = %v, want it to name the shortfall", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("a file was written despite the refusal")
	}
}

func TestRunIgnoreBudgetCollectsWhatFits(t *testing.T) {
	remaining := 4
	srv := fakeGitHub(t, testRepos(6), &remaining)
	out := filepath.Join(t.TempDir(), "bundle.md")

	err := Run([]string{"-all", "-api", srv.URL, "-out", out, "-ignore-budget", "hihipy"}, os.Stdout)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	doc, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "# r1") {
		t.Error("expected the repositories that fit to be present")
	}
	if strings.Contains(string(doc), "# r6") {
		t.Error("expected collection to stop before the last repository")
	}
}

func TestSelectReportsEveryExclusion(t *testing.T) {
	repos := []Repo{
		{Name: "keep", Size: 10},
		{Name: "forked", Size: 10, Fork: true},
		{Name: "old", Size: 10, Archived: true},
		{Name: "empty", Size: 0},
		{Name: "byname", Size: 10},
	}
	opt := options{exclude: repeatable{"byname"}}
	keep, _ := selectRepos(repos, opt, os.Stdout)
	if len(keep) != 1 || keep[0].Name != "keep" {
		t.Errorf("kept %v, want just keep", keep)
	}
}

func TestSelectSkipsOversizedRepos(t *testing.T) {
	// simonw/vaccinespotter-history is 16 GB. Reading that tarball into memory
	// would take the machine down, so it must never be reached.
	repos := []Repo{
		{Name: "normal", Size: 400},
		{Name: "vaccinespotter-history", Size: 16_492_672},
		{Name: "scrape-faa-releasable-aircraft", Size: 11_946_256},
	}
	var out strings.Builder
	keep, _ := selectRepos(repos, options{maxRepoKB: 50 * 1024}, &out)

	if len(keep) != 1 || keep[0].Name != "normal" {
		t.Fatalf("kept %v, want just the normal repository", keep)
	}
	if !strings.Contains(out.String(), "raise with -max-repo-kb") {
		t.Errorf("the report should name the flag that changes it, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "15.7 GB") {
		t.Errorf("the report should say how big, got:\n%s", out.String())
	}
}

func TestSelectAllowsAnySizeWhenCapIsZero(t *testing.T) {
	repos := []Repo{{Name: "huge", Size: 16_492_672}}
	keep, _ := selectRepos(repos, options{maxRepoKB: 0}, &strings.Builder{})
	if len(keep) != 1 {
		t.Error("a cap of zero should mean no limit")
	}
}

func TestSelectCountsRatherThanListingManySkips(t *testing.T) {
	// simonw has over 300 forks; one line each buries everything useful.
	var repos []Repo
	for i := 0; i < 300; i++ {
		repos = append(repos, Repo{Name: fmt.Sprintf("fork%d", i), Size: 10, Fork: true})
	}
	repos = append(repos, Repo{Name: "real", Size: 10})

	var out strings.Builder
	keep, _ := selectRepos(repos, options{}, &out)
	if len(keep) != 1 {
		t.Fatalf("kept %d, want 1", len(keep))
	}
	lines := strings.Count(strings.TrimSpace(out.String()), "\n") + 1
	if lines != 1 {
		t.Errorf("report ran to %d lines, want one summary line:\n%s", lines, out.String())
	}
	if !strings.Contains(out.String(), "skipped 300 fork") {
		t.Errorf("the count should lead, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "and 297 more") {
		t.Errorf("the withheld names should be counted, got:\n%s", out.String())
	}
}

func TestSelectNamesSmallGroups(t *testing.T) {
	repos := []Repo{
		{Name: "a", Size: 10, Fork: true},
		{Name: "b", Size: 10, Fork: true},
		{Name: "keep", Size: 10},
	}
	var out strings.Builder
	selectRepos(repos, options{}, &out)
	if !strings.Contains(out.String(), "a, b") {
		t.Errorf("a short list should be named in full, got:\n%s", out.String())
	}
}

func TestDryRunReservesNothingForTarballs(t *testing.T) {
	// A dry run downloads no tarballs, so holding back a request per repository
	// wrongly skips the file-type fetch when the quota would have covered it.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	remaining := 25 // enough for 22 trees, not for 22 trees plus 22 tarballs
	repos := testRepos(22)
	srv := fakeGitHubWithTrees(t, repos, &remaining)

	var out strings.Builder
	if err := Run([]string{"-all", "-api", srv.URL, "-dry-run", "hihipy"}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "fall back to the primary language") {
		t.Errorf("a dry run should still fetch file types, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "fetched and now cached") {
		t.Errorf("the fetch should have happened, got:\n%s", out.String())
	}
}

func TestTokenIsSentAndPrivateReposAreDropped(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret-token")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	var sawAuth string
	remaining := 60
	mux := http.NewServeMux()
	limits := func(w http.ResponseWriter) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Hour).Unix()))
	}
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		limits(w)
		json.NewEncoder(w).Encode(map[string]any{"rate": map[string]any{
			"limit": 5000, "remaining": remaining, "reset": time.Now().Add(time.Hour).Unix()}})
	})
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		limits(w)
		json.NewEncoder(w).Encode([]Repo{
			{Name: "public-one", FullName: "u/public-one", Size: 10, DefaultBranch: "main"},
			{Name: "private-one", FullName: "u/private-one", Size: 10, DefaultBranch: "main", Private: true},
		})
	})
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		limits(w)
		if strings.Contains(r.URL.Path, "/git/trees/") {
			json.NewEncoder(w).Encode(map[string]any{"tree": []map[string]string{
				{"path": "main.py", "type": "blob"}}})
			return
		}
		if strings.Contains(r.URL.Path, "private-one") {
			t.Error("a private repository was fetched")
		}
		w.Write(makeTarball(t, "w", map[string][]byte{"README.md": []byte("# x\n")}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var out strings.Builder
	outPath := filepath.Join(t.TempDir(), "b.md")
	if err := Run([]string{"-all", "-api", srv.URL, "-out", outPath, "u"}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}

	if sawAuth != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want the bearer token", sawAuth)
	}
	if !strings.Contains(out.String(), "(authenticated)") {
		t.Errorf("the run should say it is authenticated, got:\n%s", out.String())
	}
	if strings.Contains(out.String(), "secret-token") {
		t.Error("the token must never be printed")
	}
	doc, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "private-one") {
		t.Error("a private repository reached the bundle")
	}
}

func TestUnknownUserSaysSo(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", "59")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Hour).Unix()))
		json.NewEncoder(w).Encode(map[string]any{"rate": map[string]any{
			"limit": 60, "remaining": 59, "reset": time.Now().Add(time.Hour).Unix()}})
	})
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := Run([]string{"-all", "-api", srv.URL, "-dry-run", "thisaccountdoesnotexist12345"}, &strings.Builder{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), `no GitHub account named "thisaccountdoesnotexist12345"`) {
		t.Errorf("error = %q, want it to name the account plainly", err)
	}
	if strings.Contains(err.Error(), "per_page") {
		t.Errorf("error = %q, should not print the API URL", err)
	}
}

func TestHelpReadsAsAManual(t *testing.T) {
	// Someone who runs the tool with no arguments should learn how to use it
	// without opening the repository.
	var out strings.Builder
	fs := flag.NewFlagSet("repo-omnibus", flag.ContinueOnError)
	fs.SetOutput(&out)
	fs.String("out", "", "output path")
	usage(fs)

	for _, want := range []string{
		"Usage:", "Examples:", "repo-omnibus -all hihipy",
		"GITHUB_TOKEN", "Only public repositories are ever read",
		"What is left out of the bundle", "Flags:", "Files:",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help is missing %q", want)
		}
	}
}

func TestHelpExitsCleanly(t *testing.T) {
	// -help should print the manual and stop, without an error line or a
	// non-zero exit.
	var out strings.Builder
	if err := Run([]string{"-help"}, &out); err != nil {
		t.Errorf("Run(-help) = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Error("the manual should go to the run's own output, not stderr")
	}
	if strings.Contains(out.String(), "help requested") {
		t.Error("asking for help should not report an error")
	}
}

func TestDefaultOutIsTheDownloadsFolder(t *testing.T) {
	// A file you are about to read or drag somewhere belongs in Downloads, not
	// in whichever directory the terminal happened to be sitting in.
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := defaultOut([]string{"hihipy"})
	want := filepath.Join(home, "Downloads", "hihipy-omnibus.md")
	if got != want {
		t.Errorf("defaultOut = %q, want %q", got, want)
	}
}

func TestDefaultOutFallsBackWithoutDownloads(t *testing.T) {
	// Not every machine has a Downloads folder, and a missing one should not
	// stop a run.
	t.Setenv("HOME", t.TempDir())
	if got := defaultOut([]string{"hihipy"}); got != "hihipy-omnibus.md" {
		t.Errorf("defaultOut = %q, want the plain filename", got)
	}
}

func TestOutFlagStillWins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	remaining := 60
	srv := fakeGitHubWithTrees(t, testRepos(1), &remaining)
	out := filepath.Join(t.TempDir(), "somewhere-else.md")

	if err := Run([]string{"-all", "-api", srv.URL, "-out", out, "hihipy"}, &strings.Builder{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("-out was ignored: %v", err)
	}
}

func TestDefaultOutNamesEveryAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := defaultOut([]string{"hihipy", "charmbracelet"})
	want := filepath.Join(home, "Downloads", "hihipy-charmbracelet-omnibus.md")
	if got != want {
		t.Errorf("defaultOut = %q, want %q", got, want)
	}

	// Past three names the filename would be unreadable, so it counts instead.
	many := defaultOut([]string{"a", "b", "c", "d", "e"})
	if filepath.Base(many) != "a-and-4-more-omnibus.md" {
		t.Errorf("defaultOut for five accounts = %q", filepath.Base(many))
	}
}

func twoAccountServer(t *testing.T) *httptest.Server {
	t.Helper()
	byUser := map[string][]Repo{
		"alice": {{Name: "docs", FullName: "alice/docs", HTMLURL: "https://github.com/alice/docs",
			Size: 10, DefaultBranch: "main", Description: "Alice's notes"}},
		"bob": {{Name: "docs", FullName: "bob/docs", HTMLURL: "https://github.com/bob/docs",
			Size: 10, DefaultBranch: "main", Description: "Bob's notes"}},
	}
	mux := http.NewServeMux()
	limits := func(w http.ResponseWriter) {
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", "60")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Hour).Unix()))
	}
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		limits(w)
		json.NewEncoder(w).Encode(map[string]any{"rate": map[string]any{
			"limit": 60, "remaining": 60, "reset": time.Now().Add(time.Hour).Unix()}})
	})
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		limits(w)
		user := strings.Split(strings.TrimPrefix(r.URL.Path, "/users/"), "/")[0]
		json.NewEncoder(w).Encode(byUser[user])
	})
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		limits(w)
		if strings.Contains(r.URL.Path, "/git/trees/") {
			json.NewEncoder(w).Encode(map[string]any{"tree": []map[string]string{
				{"path": "notes.md", "type": "blob"}}})
			return
		}
		w.Write(makeTarball(t, "w", map[string][]byte{"README.md": []byte("# notes\n")}))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSeveralAccountsWriteOneFileEach(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := twoAccountServer(t)
	dir := t.TempDir()

	if err := Run([]string{"-all", "-api", srv.URL, "-out", dir, "alice", "bob"}, &strings.Builder{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, name := range []string{"alice-omnibus.md", "bob-omnibus.md"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// A file about one account names its repositories plainly.
		if !strings.Contains(string(body), "\n# docs\n") {
			t.Errorf("%s should use the bare repository name", name)
		}
	}

	alice, _ := os.ReadFile(filepath.Join(dir, "alice-omnibus.md"))
	if strings.Contains(string(alice), "Bob's notes") {
		t.Error("alice's file should not carry bob's work")
	}
}

func TestMergeWritesOneFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := twoAccountServer(t)
	out := filepath.Join(t.TempDir(), "both.md")

	if err := Run([]string{"-all", "-api", srv.URL, "-merge", "-out", out, "alice", "bob"},
		&strings.Builder{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# alice/docs", "# bob/docs", "owned by each of"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("merged file missing %q", want)
		}
	}
}

func TestOutFolderVersusOutFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	// An existing folder means "put it in here".
	if got := outPath(options{out: dir}, []string{"alice"}); got != filepath.Join(dir, "alice-omnibus.md") {
		t.Errorf("outPath with a folder = %q", got)
	}
	// Anything else is the filename itself.
	file := filepath.Join(dir, "named.md")
	if got := outPath(options{out: file}, []string{"alice"}); got != file {
		t.Errorf("outPath with a file = %q", got)
	}
	// A trailing separator means a folder, even one that does not exist yet.
	pending := filepath.Join(dir, "new") + string(filepath.Separator)
	if got := outPath(options{out: pending}, []string{"alice"}); got != filepath.Join(pending, "alice-omnibus.md") {
		t.Errorf("outPath with a trailing separator = %q", got)
	}
}

func TestAccountsFilteredToNothingAreNamed(t *testing.T) {
	// An account whose repositories are all forks produces no file. Saying so
	// is the difference between a filter and a disappearance.
	repos := []Repo{
		{Name: "keeper", FullName: "alice/keeper", Size: 10},
		{Name: "a-fork", FullName: "bob/a-fork", Size: 10, Fork: true},
	}
	var out strings.Builder
	keep, dropped := selectRepos(repos, options{}, &out)
	reportEmptyAccounts([]string{"alice", "bob"}, keep, dropped, &out)

	if !strings.Contains(out.String(), "bob: nothing to collect") {
		t.Errorf("the emptied account should be named, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1 forks") {
		t.Errorf("it should say what happened to that account, got:\n%s", out.String())
	}
	if strings.Contains(out.String(), "alice: nothing") {
		t.Error("an account that kept something should not be reported")
	}
	if !strings.Contains(out.String(), "-include-forks") {
		t.Error("the report should name the flag that would widen it")
	}
	if strings.Contains(out.String(), "-max-repo-kb") {
		t.Error("it should only name flags that apply to what happened")
	}
}

func TestPickingIsTheDefaultAndAllSkipsIt(t *testing.T) {
	// The tests run without a terminal, which is the same position a pipeline
	// is in: it must collect rather than hang waiting for a keypress.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	original := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = original })

	remaining := 60
	srv := fakeGitHubWithTrees(t, testRepos(2), &remaining)
	out := filepath.Join(t.TempDir(), "b.md")

	var log strings.Builder
	if err := Run([]string{"-api", srv.URL, "-out", out, "hihipy"}, &log); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(log.String(), "no terminal to ask through") {
		t.Errorf("a run without a terminal should say why it did not ask:\n%s", log.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("it should still have written the file: %v", err)
	}
}

func TestGroupByOwnerKeepsCommandLineOrder(t *testing.T) {
	repos := []Repo{
		{Name: "x", FullName: "bob/x"},
		{Name: "y", FullName: "alice/y"},
		{Name: "z", FullName: "bob/z"},
	}
	got := groupByOwner(repos, []string{"alice", "bob"})
	if len(got) != 2 || got[0].owner != "alice" || got[1].owner != "bob" {
		t.Fatalf("groups = %+v, want alice then bob", got)
	}
	if len(got[1].repos) != 2 {
		t.Errorf("bob has %d repositories, want 2", len(got[1].repos))
	}
}

func TestSkipLinesNameTheOwnerWithSeveralAccounts(t *testing.T) {
	// A bare repository name in a shared skip list does not say whose it is,
	// and a reader will attribute it to the wrong account.
	repos := []Repo{
		{Name: "keeper", FullName: "alice/keeper", Size: 10},
		{Name: "arcgis_arboretum", FullName: "bob/arcgis_arboretum", Size: 51_900},
	}
	var many strings.Builder
	selectRepos(repos, options{users: []string{"alice", "bob"}, maxRepoKB: 50 * 1024}, &many)
	if !strings.Contains(many.String(), "bob/arcgis_arboretum") {
		t.Errorf("the skip line should name the owner, got:\n%s", many.String())
	}

	// With one account the owner is already known, so the name stays short.
	var one strings.Builder
	selectRepos(repos[1:], options{users: []string{"bob"}, maxRepoKB: 50 * 1024}, &one)
	if strings.Contains(one.String(), "bob/arcgis_arboretum") {
		t.Errorf("a single account should not repeat itself, got:\n%s", one.String())
	}
}

func TestFilteredLinesNameTheRepositories(t *testing.T) {
	// Daniella has two repositories and one is too large. Her picker shows one
	// row, and a count alone would not say which one is missing.
	lines := filteredLines(map[string][]string{
		"over 50.0 MB (raise with -max-repo-kb)": {"arcgis_arboretum (50.7 MB)"},
	}, false)
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want one", lines)
	}
	for _, want := range []string{"1 too large", "arcgis_arboretum", "-max-repo-kb"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("line = %q, missing %q", lines[0], want)
		}
	}
	if filteredLines(nil, false) != nil {
		t.Error("an account that lost nothing should get no lines")
	}
}

func TestFilteredLinesCutLongListsUnlessVerbose(t *testing.T) {
	// whinis has 24 forks. Naming all of them buries the list they came for.
	var forks []string
	for i := 0; i < 24; i++ {
		forks = append(forks, fmt.Sprintf("fork%d", i))
	}
	reasons := map[string][]string{"fork": forks, "archived": {"eve-jacknife"}}

	short := filteredLines(reasons, false)
	if !strings.Contains(short[0], "and 21 more") {
		t.Errorf("long list should be cut, got %q", short[0])
	}
	if !strings.Contains(short[1], "eve-jacknife") {
		t.Errorf("a single name should be given in full, got %q", short[1])
	}

	full := filteredLines(reasons, true)
	if strings.Contains(full[0], "more") {
		t.Errorf("-verbose should name them all, got %q", full[0])
	}
	if !strings.Contains(full[0], "fork23") {
		t.Errorf("-verbose should reach the last one, got %q", full[0])
	}
}

func TestAccountInfoCountsAndExplainsWhatIsMissing(t *testing.T) {
	repos := []Repo{
		{Name: "kept", FullName: "hihipy/kept", HTMLURL: "https://github.com/hihipy/kept", Size: 10},
		{Name: "a-fork", FullName: "hihipy/a-fork", HTMLURL: "https://github.com/hihipy/a-fork", Size: 10, Fork: true},
		{Name: "big", FullName: "hihipy/big", HTMLURL: "https://github.com/hihipy/big", Size: 60_000},
	}
	var out strings.Builder
	keep, dropped := selectRepos(repos, options{maxRepoKB: 50 * 1024}, &out)
	info := accountInfo(repos, dropped)

	if info["hihipy"].Total != 3 {
		t.Errorf("Total = %d, want all 3 counted before filtering", info["hihipy"].Total)
	}

	reasons := map[string]string{}
	for _, l := range info["hihipy"].Left {
		reasons[l.Name] = l.Reason
	}
	if reasons["a-fork"] != "a fork of someone else's project" {
		t.Errorf("fork reason = %q", reasons["a-fork"])
	}
	if !strings.Contains(reasons["big"], "too large") {
		t.Errorf("size reason = %q, want it readable", reasons["big"])
	}
	if strings.Contains(reasons["big"], "raise with") {
		t.Errorf("size reason = %q, should not carry a flag hint into the document", reasons["big"])
	}

	// Unticking is a different reason from filtering, and both must show.
	noteUnpicked(info, keep, nil)
	var sawUnpicked bool
	for _, l := range info["hihipy"].Left {
		if l.Name == "kept" && l.Reason == "not selected" {
			sawUnpicked = true
		}
	}
	if !sawUnpicked {
		t.Errorf("an unticked repository should be recorded, got %+v", info["hihipy"].Left)
	}
}
