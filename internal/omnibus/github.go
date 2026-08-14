package omnibus

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

const userAgent = "RepoOmnibus (+https://github.com/hihipy/repo-omnibus)"

// Repo is the subset of GitHub's repository object the bundle needs.
type Repo struct {
	Name          string   `json:"name"`
	FullName      string   `json:"full_name"`
	HTMLURL       string   `json:"html_url"`
	Description   string   `json:"description"`
	Homepage      string   `json:"homepage"`
	Language      string   `json:"language"`
	Topics        []string `json:"topics"`
	Stars         int      `json:"stargazers_count"`
	Size          int      `json:"size"` // kilobytes, as GitHub reports it
	Private       bool     `json:"private"`
	Fork          bool     `json:"fork"`
	Archived      bool     `json:"archived"`
	DefaultBranch string   `json:"default_branch"`
	CreatedAt     string   `json:"created_at"`
	PushedAt      string   `json:"pushed_at"`
	License       *struct {
		SPDXID string `json:"spdx_id"`
	} `json:"license"`

	// Langs is every language GitHub detects, heaviest first, and Exts is the
	// file-extension histogram. Neither comes from the listing: both cost one
	// request per repository, so they are filled in separately.
	Langs []string   `json:"-"`
	Exts  []ExtCount `json:"-"`
}

// ExtCount is one file extension and how many files carry it.
type ExtCount struct {
	Ext   string
	Count int
}

// PrimaryLang is the language a repository is filed under. It is the listing's
// own field, and falls back to the heaviest of the detected languages.
func (r Repo) PrimaryLang() string {
	if r.Language != "" {
		return r.Language
	}
	if len(r.Langs) > 0 {
		return r.Langs[0]
	}
	return ""
}

// LangLabel describes what a repository is made of. File extensions come first
// where they are known, because GitHub's language classifier collapses detail
// that matters: eight SQL dialect scripts all report as one language, while
// "8x.sql" says what is actually there. Without the histogram it falls back to
// the language names, and a repository GitHub files under nothing reads as a
// dash.
func (r Repo) LangLabel() string {
	if len(r.Exts) > 0 {
		parts := make([]string, 0, len(r.Exts))
		for _, e := range r.Exts {
			parts = append(parts, fmt.Sprintf("%dx%s", e.Count, e.Ext))
		}
		return strings.Join(parts, ", ")
	}
	if len(r.Langs) > 0 {
		return strings.Join(r.Langs, ", ")
	}
	if r.Language != "" {
		return r.Language
	}
	return "-"
}

// SortRepos orders by name, case-insensitively.
func SortRepos(repos []Repo) {
	sort.SliceStable(repos, func(i, j int) bool {
		return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name)
	})
}

// Budget mirrors the X-RateLimit headers, so the running count comes from
// GitHub rather than from a tally kept here.
type Budget struct {
	Limit     int
	Remaining int
	Reset     time.Time
	Known     bool
}

// ResetIn renders the wait until the quota refills, for use in messages.
func (b Budget) ResetIn() string {
	if !b.Known || b.Reset.IsZero() {
		return "unknown"
	}
	d := time.Until(b.Reset)
	if d <= 0 {
		return "now"
	}
	return fmt.Sprintf("%d min", int(d.Minutes()+0.5))
}

// Client talks to the GitHub REST API. A token raises the rate limit from 60
// requests an hour to 5,000; it does not widen what is visible, because the
// listing endpoint returns only public repositories whoever asks. PublicRepos
// drops anything marked private regardless, so the guarantee does not rest on
// that behaviour alone.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Budget  Budget
	token   string
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 5 * time.Minute},
		token:   findToken(),
	}
}

// findToken reads the usual environment variables. A token is never accepted as
// a flag, which would put it in shell history and in the process list.
func findToken() string {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}

// Authenticated reports whether a token was found, for the run's own report. It
// never returns the token itself.
func (c *Client) Authenticated() bool { return c != nil && c.token != "" }

func (c *Client) noteLimits(h http.Header) {
	limit, errL := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	remaining, errR := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	if errL != nil || errR != nil {
		return
	}
	c.Budget.Limit = limit
	c.Budget.Remaining = remaining
	c.Budget.Known = true
	if sec, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		c.Budget.Reset = time.Unix(sec, 0)
	}
}

func (c *Client) get(url, accept string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	c.noteLimits(resp.Header)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &APIError{URL: url, Status: resp.StatusCode}
	}
	return resp, nil
}

// APIError carries the status code so callers can treat 403 (rate limited) and
// 404 (gone or renamed) differently from a generic failure.
type APIError struct {
	URL    string
	Status int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("HTTP %d from %s", e.Status, e.URL)
}

// RateLimit reads the current quota. This endpoint does not itself consume
// quota, so it is safe to call before deciding whether to start.
func (c *Client) RateLimit() (Budget, error) {
	resp, err := c.get(c.BaseURL+"/rate_limit", "application/vnd.github+json")
	if err != nil {
		return Budget{}, err
	}
	defer resp.Body.Close()

	var payload struct {
		Rate struct {
			Limit     int   `json:"limit"`
			Remaining int   `json:"remaining"`
			Reset     int64 `json:"reset"`
		} `json:"rate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Budget{}, err
	}
	return Budget{
		Limit:     payload.Rate.Limit,
		Remaining: payload.Rate.Remaining,
		Reset:     time.Unix(payload.Rate.Reset, 0),
		Known:     true,
	}, nil
}

// PublicRepos lists every repository the account owns. This endpoint returns
// public repositories only to an unauthenticated caller, and no token is sent.
func (c *Client) PublicRepos(user string) ([]Repo, error) {
	var all []Repo
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/users/%s/repos?per_page=100&type=owner&page=%d",
			c.BaseURL, user, page)
		resp, err := c.get(url, "application/vnd.github+json")
		if err != nil {
			return nil, err
		}
		var batch []Repo
		err = json.NewDecoder(resp.Body).Decode(&batch)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, r := range batch {
			// Belt and braces: this endpoint does not return private
			// repositories, and the tool would not include them if it did.
			if !r.Private {
				all = append(all, r)
			}
		}
		if len(batch) < 100 {
			return all, nil
		}
	}
}

// Tarball downloads one repository as a gzipped tar of its default branch at
// HEAD, which costs a single request no matter how many files it holds.
func (c *Client) Tarball(r Repo) ([]byte, error) {
	branch := r.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	url := fmt.Sprintf("%s/repos/%s/tarball/%s", c.BaseURL, r.FullName, branch)
	resp, err := c.get(url, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// Languages returns every language GitHub detects in a repository, heaviest
// first. It costs one request per repository, which is why it is opt in.
func (c *Client) Languages(r Repo) ([]string, error) {
	url := fmt.Sprintf("%s/repos/%s/languages", c.BaseURL, r.FullName)
	resp, err := c.get(url, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var bytesByLang map[string]int64
	if err := json.NewDecoder(resp.Body).Decode(&bytesByLang); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(bytesByLang))
	for name := range bytesByLang {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if bytesByLang[names[i]] != bytesByLang[names[j]] {
			return bytesByLang[names[i]] > bytesByLang[names[j]]
		}
		return names[i] < names[j]
	})
	return names, nil
}

// countedExts are the extensions worth counting: the ones that say what a
// repository is made of. Documentation, CI config, and packaging appear in
// every repository and so distinguish none of them, which is why .md and .yml
// are absent here. Binary assets are absent for the same reason.
var countedExts = map[string]bool{
	".py": true, ".r": true, ".rmd": true, ".qmd": true, ".ipynb": true,
	".sql": true, ".sh": true, ".zsh": true, ".bash": true, ".ps1": true,
	".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".jsx": true,
	".html": true, ".css": true, ".scss": true, ".sass": true,
	".go": true, ".rs": true, ".java": true, ".c": true, ".h": true,
	".cpp": true, ".hpp": true, ".cs": true, ".csx": true, ".rb": true,
	".php": true, ".swift": true, ".lua": true, ".pl": true, ".kt": true,
	".bas": true, ".frm": true, ".cls": true, ".vba": true, ".vb": true,
	".tex": true, ".sty": true, ".bib": true, ".dax": true, ".m": true, ".pq": true,
	".json": true, ".xlsx": true, ".xlsm": true, ".docx": true, ".csv": true,
}

// ExtHistogram counts the extensions worth reporting, commonest first, keeping
// at most max entries so one repository cannot widen the whole listing.
func ExtHistogram(paths []string, max int) []ExtCount {
	counts := map[string]int{}
	for _, p := range paths {
		ext := strings.ToLower(path.Ext(p))
		if countedExts[ext] {
			counts[ext]++
		}
	}

	out := make([]ExtCount, 0, len(counts))
	for ext, n := range counts {
		out = append(out, ExtCount{Ext: ext, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Ext < out[j].Ext
	})
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// Tree lists every file path in a repository at HEAD. One request returns the
// whole tree, which is what makes an extension histogram affordable.
func (c *Client) Tree(r Repo) ([]string, error) {
	branch := r.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	url := fmt.Sprintf("%s/repos/%s/git/trees/%s?recursive=1", c.BaseURL, r.FullName, branch)
	resp, err := c.get(url, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	var paths []string
	for _, entry := range payload.Tree {
		if entry.Type == "blob" {
			paths = append(paths, entry.Path)
		}
	}
	return paths, nil
}
