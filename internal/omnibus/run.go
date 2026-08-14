package omnibus

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultAPI = "https://api.github.com"

// repeatable collects a flag given more than once, as -exclude a -exclude b.
type repeatable []string

func (r *repeatable) String() string { return strings.Join(*r, ",") }

func (r *repeatable) Set(v string) error {
	*r = append(*r, v)
	return nil
}

type options struct {
	user            string
	out             string
	exclude         repeatable
	includeForks    bool
	includeArchived bool
	maxFileBytes    int64
	maxRepoKB       int
	verbose         bool
	dryRun          bool
	ignoreBudget    bool
	pick            bool
	plain           bool
	includeLicenses bool
	includeGen      bool
	skipOffice      bool
	noFileTypes     bool
	apiURL          string
}

// Run executes the command with the given arguments, writing progress to out.
func Run(args []string, out io.Writer) error {
	var opt options

	fs := flag.NewFlagSet("repo-omnibus", flag.ContinueOnError)
	// The manual is output, not a diagnostic, so it goes where the rest of the
	// run reports rather than to stderr.
	fs.SetOutput(out)
	fs.StringVar(&opt.out, "out", "",
		"output path (default ~/Downloads/<user>-omnibus.md)")
	fs.Var(&opt.exclude, "exclude", "repository name to leave out; repeatable")
	fs.BoolVar(&opt.includeForks, "include-forks", false, "include forks")
	fs.BoolVar(&opt.includeArchived, "include-archived", false, "include archived repositories")
	fs.Int64Var(&opt.maxFileBytes, "max-file-bytes", 512*1024, "skip files larger than this")
	fs.IntVar(&opt.maxRepoKB, "max-repo-kb", 50*1024,
		"skip repositories larger than this many KB on GitHub; 0 for no limit")
	fs.BoolVar(&opt.verbose, "verbose", false,
		"name every skipped repository instead of counting them")
	fs.BoolVar(&opt.dryRun, "dry-run", false, "report cost and size, then write nothing")
	fs.BoolVar(&opt.pick, "pick", false,
		"list the repositories and ask which ones to collect")
	fs.BoolVar(&opt.noFileTypes, "no-file-types", false,
		"skip the per-repository file-type counts, which cost one request each")
	fs.BoolVar(&opt.includeGen, "include-generated", false,
		"include generated files: lock files, vendored code, and anything whose shape says a machine wrote it")
	fs.BoolVar(&opt.includeLicenses, "include-licenses", false,
		"include LICENSE files, which are long and identical across repositories")
	fs.BoolVar(&opt.skipOffice, "skip-office", false,
		"link spreadsheets and documents instead of extracting their text")
	fs.BoolVar(&opt.plain, "plain", false,
		"with -pick, type a selection instead of using arrow keys")
	fs.BoolVar(&opt.ignoreBudget, "ignore-budget", false,
		"start even when the quota looks too small, and stop when it runs out")
	fs.StringVar(&opt.apiURL, "api", defaultAPI, "API base URL, for testing")
	fs.Usage = func() { usage(fs) }
	if err := fs.Parse(args); err != nil {
		// Asking for help is not a failure: the manual has been printed and
		// there is nothing left to report.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("expected exactly one GitHub username")
	}
	opt.user = fs.Arg(0)
	if opt.out == "" {
		opt.out = defaultOut(opt.user)
	}

	client := NewClient(opt.apiURL)

	quota, err := client.RateLimit()
	if err != nil {
		return fmt.Errorf("could not reach the GitHub API: %w", err)
	}
	auth := "unauthenticated"
	if client.Authenticated() {
		auth = "authenticated"
	}
	fmt.Fprintf(out, "rate limit: %d of %d requests left, resets in %s (%s)\n",
		quota.Remaining, quota.Limit, quota.ResetIn(), auth)
	if !client.Authenticated() && quota.Limit <= 60 {
		fmt.Fprintln(out, "set GITHUB_TOKEN for 5,000 requests an hour; "+
			"only public repositories are read either way")
	}
	if quota.Remaining < 2 {
		return fmt.Errorf("not enough quota to list repositories. Wait %s and run again",
			quota.ResetIn())
	}

	repos, err := client.PublicRepos(opt.user)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			switch apiErr.Status {
			case http.StatusNotFound:
				return fmt.Errorf("no GitHub account named %q", opt.user)
			case http.StatusForbidden, http.StatusTooManyRequests:
				return fmt.Errorf("GitHub rate limit reached, resets in %s. "+
					"Set GITHUB_TOKEN to raise it to 5,000 an hour", client.Budget.ResetIn())
			case http.StatusUnauthorized:
				return errors.New("GitHub rejected the token in GITHUB_TOKEN")
			}
		}
		return fmt.Errorf("listing repositories failed: %w", err)
	}
	if len(repos) == 0 {
		return fmt.Errorf("%q has no public repositories", opt.user)
	}

	keep := selectRepos(repos, opt, out)
	if len(keep) == 0 {
		return errors.New("no repositories to collect")
	}

	SortRepos(keep)

	if !opt.noFileTypes {
		fillFileTypes(client, keep, opt, out)
	}

	if opt.pick {
		if !interactive() {
			return errors.New("-pick needs a terminal; use -exclude when scripting")
		}
		in := io.Reader(os.Stdin)
		if opt.plain {
			in = struct{ io.Reader }{os.Stdin} // hides os.Stdin, so Pick types
		}
		keep, err = Pick(keep, in, out)
		if err != nil {
			return err
		}
		if len(keep) == 0 {
			return errors.New("no repositories selected")
		}
	}

	// One request per repository, since each arrives as a single tarball.
	needed := len(keep)
	left := quota.Remaining
	if client.Budget.Known {
		left = client.Budget.Remaining
	}
	var kb int
	for _, r := range keep {
		kb += r.Size
	}
	verdict := "enough"
	if left < needed {
		verdict = "NOT enough"
	}
	fmt.Fprintf(out, "\n%d repositories, %s KB on GitHub\n", len(keep), commas(int64(kb)))
	fmt.Fprintf(out, "cost: %d requests, %d left, %s\n", needed, left, verdict)
	// The ratio is calibrated on source repositories. A repository of scraped
	// data is mostly oversized files that never enter the bundle, so this is an
	// upper bound rather than a forecast.
	fmt.Fprintf(out, "output: at most %s, %s tokens, before binaries and oversized files are dropped\n",
		humanBytes(int64(kb)*1024/2), commas(int64(kb)*1024/8))

	if opt.dryRun {
		shown := keep
		if len(shown) > 20 {
			shown = shown[:20]
		}
		for _, r := range shown {
			fmt.Fprintf(out, "take   %s: %s KB\n", r.Name, commas(int64(r.Size)))
		}
		if len(keep) > len(shown) {
			fmt.Fprintf(out, "and %d more\n", len(keep)-len(shown))
		}
		fmt.Fprintln(out, "\nDry run, nothing written.")
		return nil
	}

	if left < needed && !opt.ignoreBudget {
		return fmt.Errorf("stopping: %d requests needed, %d left, resets in %s. "+
			"Re-run later, narrow with -exclude, or pass -ignore-budget to collect what fits",
			needed, left, client.Budget.ResetIn())
	}

	bundles, complete := collect(client, keep, opt, out)
	if len(bundles) == 0 {
		return errors.New("nothing collected")
	}
	if !complete {
		fmt.Fprintf(out, "\nincomplete: %d of %d repositories collected\n",
			len(bundles), len(keep))
	}

	fmt.Fprint(out, TerminalSummary(bundles, 8))

	doc := Render(opt.user, bundles, time.Now())
	if dir := filepath.Dir(opt.out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(opt.out, []byte(doc), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", opt.out, err)
	}
	fmt.Fprintf(out, "\nwrote %s (%s bytes, %d repositories)\n",
		opt.out, commas(int64(len(doc))), len(bundles))
	return nil
}

// defaultOut puts the bundle in the user's Downloads folder, which is where a
// file you are about to open, read, or drag into something else belongs. The
// working directory is where the tool happens to be run from, which is rarely
// the same place. It falls back to the working directory when there is no home
// directory or no Downloads folder in it.
func defaultOut(user string) string {
	name := user + "-omnibus.md"
	home, err := os.UserHomeDir()
	if err != nil {
		return name
	}
	downloads := filepath.Join(home, "Downloads")
	if info, err := os.Stat(downloads); err != nil || !info.IsDir() {
		return name
	}
	return filepath.Join(downloads, name)
}

// usage is the manual. A tool nobody can read is a tool nobody runs, and the
// terminal is where a reader looks first.
func usage(fs *flag.FlagSet) {
	out := fs.Output()
	fmt.Fprint(out, `repo-omnibus collects every public repository an account owns into one
Markdown file, readable on its own and usable as context for a language model.

Usage:
  repo-omnibus [flags] <github-user>

Examples:
  repo-omnibus hihipy
        Collect everything, write ~/Downloads/hihipy-omnibus.md

  repo-omnibus -pick hihipy
        Choose which repositories to collect, with arrow keys

  repo-omnibus -dry-run torvalds
        Report what it would cost and how big it would be, write nothing

  repo-omnibus -out ~/Desktop/charm.md charmbracelet
        Works on organizations, and -out chooses where the file lands

  repo-omnibus -exclude notes -exclude scratch hihipy
        Leave named repositories out

Rate limits:
  GitHub allows 60 requests an hour without a token and 5,000 with one. A run
  costs one request per repository, plus one more for each repository whose
  file list is not already cached.

        export GITHUB_TOKEN=$(gh auth token)

  Only public repositories are ever read. A token raises the limit; it does
  not widen what is visible.

Rights:
  A bundle contains copies of files whose copyright belongs to their authors.
  Public does not mean unlicensed. Licence files are omitted by default, so a
  bundle is not a licensed distribution of anything in it. Keep it as a private
  working copy and go to each repository for terms before reusing or
  republishing any part of it. -include-licenses keeps the licence files.

What is left out of the bundle, and why:
  Binary files, because they are not text. Files over the size limit. License
  text, identical in every repository. Dependency lock files. Anything a tool
  marked as generated. Vendored and built directories. And any file whose
  shape says a machine wrote it: one enormous line, or thousands of nearly
  identical ones. Every exclusion is counted in the summary and listed with a
  reason in the file itself, so nothing disappears quietly.

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprint(out, `
Files:
  ~/Downloads/<user>-omnibus.md
                               the bundle, unless -out says otherwise
  ~/Library/Caches/repo-omnibus/trees.json
                               remembers each repository's file list, so a
                               rerun costs nothing until someone pushes
`)
}

// fillFileTypes gives each repository its extension histogram, reading the
// cache first so an unchanged repository costs nothing. Only what is left after
// the cache is fetched, and only if the quota can spare it alongside the
// tarballs still to come.
func fillFileTypes(client *Client, keep []Repo, opt options, out io.Writer) {
	cache := OpenTreeCache()

	var missing []int
	for i := range keep {
		if paths, ok := cache.Get(keep[i]); ok {
			keep[i].Exts = ExtHistogram(paths, 6)
		} else {
			missing = append(missing, i)
		}
	}
	if len(missing) == 0 {
		if cache.Len() > 0 {
			fmt.Fprintf(out, "file types: all %d from cache, no requests spent\n", len(keep))
		}
		return
	}

	// One request per repository still to fetch, and one tarball each after
	// that. A dry run downloads nothing, so it reserves nothing.
	reserve := len(keep)
	if opt.dryRun {
		reserve = 0
	}
	left := client.Budget.Remaining
	if !client.Budget.Known {
		left = len(missing) + reserve // unknown quota: assume it is enough
	}
	if left < len(missing)+reserve {
		fmt.Fprintf(out, "file types: %d cached, %d would cost a request each and "+
			"only %d are left, so those fall back to the primary language\n",
			len(keep)-len(missing), len(missing), left)
		return
	}

	fetched := 0
	for _, i := range missing {
		paths, err := client.Tree(keep[i])
		if err != nil {
			fmt.Fprintf(out, "file types unavailable for %s: %v\n", keep[i].Name, err)
			break
		}
		keep[i].Exts = ExtHistogram(paths, 6)
		cache.Put(keep[i], paths)
		fetched++
	}
	if err := cache.Save(); err != nil {
		fmt.Fprintf(out, "cache not written (%v), the next run will fetch again\n", err)
	}
	if fetched > 0 {
		fmt.Fprintf(out, "file types: %d cached, %d fetched and now cached\n",
			len(keep)-len(missing), fetched)
	}
}

// selectRepos applies the exclusions and reports what it dropped. An account
// with hundreds of forks would bury the useful output under one line each, so
// reasons are counted and only small groups are named.
func selectRepos(repos []Repo, opt options, out io.Writer) []Repo {
	excluded := make(map[string]bool, len(opt.exclude))
	for _, name := range opt.exclude {
		excluded[name] = true
	}

	var keep []Repo
	dropped := map[string][]string{}
	var order []string
	drop := func(reason, name string) {
		if _, seen := dropped[reason]; !seen {
			order = append(order, reason)
		}
		dropped[reason] = append(dropped[reason], name)
	}

	for _, r := range repos {
		switch {
		case excluded[r.Name]:
			drop("excluded by name", r.Name)
		case r.Fork && !opt.includeForks:
			drop("fork", r.Name)
		case r.Archived && !opt.includeArchived:
			drop("archived", r.Name)
		case r.Size == 0:
			drop("empty", r.Name)
		case opt.maxRepoKB > 0 && r.Size > opt.maxRepoKB:
			drop(fmt.Sprintf("over %s (raise with -max-repo-kb)",
				humanBytes(int64(opt.maxRepoKB)*1024)),
				fmt.Sprintf("%s (%s)", r.Name, humanBytes(int64(r.Size)*1024)))
		default:
			keep = append(keep, r)
		}
	}

	for _, reason := range order {
		names := dropped[reason]
		if opt.verbose || len(names) <= 4 {
			fmt.Fprintf(out, "skipped %d %s: %s\n", len(names), reason, strings.Join(names, ", "))
			continue
		}
		fmt.Fprintf(out, "skipped %d %s: %s and %d more\n",
			len(names), reason, strings.Join(names[:3], ", "), len(names)-3)
	}
	return keep
}

// collect downloads each repository, stopping cleanly when the quota runs out
// rather than failing partway through. The bool reports whether all of them
// were collected.
func collect(client *Client, keep []Repo, opt options, out io.Writer) ([]Bundle, bool) {
	var bundles []Bundle
	for i, r := range keep {
		if client.Budget.Known && client.Budget.Remaining < 1 {
			fmt.Fprintf(out, "stop   quota exhausted with %d repositories left, resets in %s\n",
				len(keep)-i, client.Budget.ResetIn())
			return bundles, false
		}

		data, err := client.Tarball(r)
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden {
				fmt.Fprintf(out, "stop   %s: rate limited, resets in %s\n",
					r.Name, client.Budget.ResetIn())
				return bundles, false
			}
			fmt.Fprintf(out, "skip   %s: %v\n", r.Name, err)
			continue
		}

		b, err := ReadTarball(data, ReadOptions{
			MaxBytes:         opt.maxFileBytes,
			IncludeLicenses:  opt.includeLicenses,
			IncludeGenerated: opt.includeGen,
			SkipOffice:       opt.skipOffice,
		})
		if err != nil {
			fmt.Fprintf(out, "skip   %s: %v\n", r.Name, err)
			continue
		}
		b.Repo = r
		bundles = append(bundles, b)

		tail := ""
		if client.Budget.Known {
			tail = fmt.Sprintf(", %d requests left", client.Budget.Remaining)
		}
		fmt.Fprintf(out, "take   %s: %d files, %d skipped%s\n",
			r.Name, len(b.Files), len(b.Skipped), tail)
	}
	return bundles, true
}
