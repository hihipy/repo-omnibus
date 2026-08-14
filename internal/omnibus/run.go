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
	users           []string
	out             string
	exclude         repeatable
	includeForks    bool
	includeArchived bool
	maxFileBytes    int64
	maxRepoKB       int
	verbose         bool
	dryRun          bool
	merge           bool
	ignoreBudget    bool
	all             bool
	dropped         map[string]map[string][]string
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
	fs.BoolVar(&opt.merge, "merge", false,
		"with several accounts, write one combined file instead of one per account")
	fs.BoolVar(&opt.all, "all", false,
		"collect everything without asking, which is the default when there is no terminal")
	fs.BoolVar(&opt.noFileTypes, "no-file-types", false,
		"skip the per-repository file-type counts, which cost one request each")
	fs.BoolVar(&opt.includeGen, "include-generated", false,
		"include generated files: lock files, vendored code, and anything whose shape says a machine wrote it")
	fs.BoolVar(&opt.includeLicenses, "include-licenses", false,
		"include LICENSE files, which are long and identical across repositories")
	fs.BoolVar(&opt.skipOffice, "skip-office", false,
		"link spreadsheets and documents instead of extracting their text")
	fs.BoolVar(&opt.plain, "plain", false,
		"type a selection instead of using arrow keys")
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
	if fs.NArg() == 0 {
		fs.Usage()
		return errors.New("name at least one GitHub account")
	}
	opt.users = fs.Args()

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

	// Several accounts are collected into one document, which is the point of
	// naming more than one: a personal account and a work organization read
	// better together than as two files.
	var repos []Repo
	for _, user := range opt.users {
		batch, err := client.PublicRepos(user)
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				switch apiErr.Status {
				case http.StatusNotFound:
					return fmt.Errorf("no GitHub account named %q", user)
				case http.StatusForbidden, http.StatusTooManyRequests:
					return fmt.Errorf("GitHub rate limit reached, resets in %s. "+
						"Set GITHUB_TOKEN to raise it to 5,000 an hour", client.Budget.ResetIn())
				case http.StatusUnauthorized:
					return errors.New("GitHub rejected the token in GITHUB_TOKEN")
				}
			}
			return fmt.Errorf("listing repositories for %s failed: %w", user, err)
		}
		if len(batch) == 0 {
			return fmt.Errorf("%q has no public repositories", user)
		}
		repos = append(repos, batch...)
	}

	keep, dropped := selectRepos(repos, opt, out)
	opt.dropped = dropped
	info := accountInfo(repos, dropped)
	reportEmptyAccounts(opt.users, keep, dropped, out)
	if len(keep) == 0 {
		return errors.New("no repositories to collect")
	}

	SortRepos(keep)

	if !opt.noFileTypes {
		fillFileTypes(client, keep, opt, out)
	}

	// Asking is the default, because collecting an account you have not looked
	// at is how a 30 MB repository ends up in a bundle. A pipeline has no
	// terminal to ask through, so it takes everything and says so.
	if !opt.all {
		if !stdinIsTerminal() {
			fmt.Fprintln(out, "no terminal to ask through, collecting everything")
		} else {
			offered := keep
			keep, err = pickPerAccount(keep, opt, out)
			if err != nil {
				return err
			}
			noteUnpicked(info, offered, keep)
			if len(keep) == 0 {
				return errors.New("nothing selected")
			}
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

	return write(bundles, info, opt, out)
}

// write saves the bundles. Several accounts get a file each, because two
// people's work read together is rarely one document; -merge asks for the
// combined file instead.
func write(bundles []Bundle, info map[string]AccountInfo, opt options, out io.Writer) error {
	groups := groupBundlesByOwner(bundles, opt.users)
	if opt.merge || len(groups) == 1 {
		return writeOne(opt.users, bundles, info, outPath(opt, opt.users), out)
	}
	for _, g := range groups {
		if err := writeOne([]string{g.owner}, g.bundles, info,
			outPath(opt, []string{g.owner}), out); err != nil {
			return err
		}
	}
	return nil
}

type bundleGroup struct {
	owner   string
	bundles []Bundle
}

type repoGroup struct {
	owner string
	repos []Repo
}

// groupBundlesByOwner and groupByOwner both keep the accounts in the order they
// were named on the command line, which is the order the person asking has in
// mind.
func groupBundlesByOwner(bundles []Bundle, users []string) []bundleGroup {
	byOwner := map[string][]Bundle{}
	var order []string
	for _, b := range bundles {
		owner := ownerOf(b.Repo)
		if _, seen := byOwner[owner]; !seen {
			order = append(order, owner)
		}
		byOwner[owner] = append(byOwner[owner], b)
	}

	var out []bundleGroup
	for _, owner := range orderOwners(order, users) {
		out = append(out, bundleGroup{owner, byOwner[owner]})
	}
	return out
}

func groupByOwner(repos []Repo, users []string) []repoGroup {
	byOwner := map[string][]Repo{}
	var order []string
	for _, r := range repos {
		owner := ownerOf(r)
		if _, seen := byOwner[owner]; !seen {
			order = append(order, owner)
		}
		byOwner[owner] = append(byOwner[owner], r)
	}

	var out []repoGroup
	for _, owner := range orderOwners(order, users) {
		out = append(out, repoGroup{owner, byOwner[owner]})
	}
	return out
}

// orderOwners puts the owners in the order the accounts were named, then any
// the loop missed, in case an account was renamed under us.
func orderOwners(present, users []string) []string {
	var out []string
	used := map[string]bool{}
	for _, u := range users {
		for _, owner := range present {
			if strings.EqualFold(owner, u) && !used[owner] {
				out = append(out, owner)
				used[owner] = true
			}
		}
	}
	for _, owner := range present {
		if !used[owner] {
			out = append(out, owner)
			used[owner] = true
		}
	}
	return out
}

func ownerOf(r Repo) string {
	if i := strings.Index(r.FullName, "/"); i > 0 {
		return r.FullName[:i]
	}
	return r.FullName
}

// outPath decides where one document goes. Without -out that is the Downloads
// folder; with -out it is the file named, or a file inside the folder named.
func outPath(opt options, users []string) string {
	name := filepath.Base(defaultOut(users))
	if opt.out == "" {
		return defaultOut(users)
	}
	if info, err := os.Stat(opt.out); err == nil && info.IsDir() {
		return filepath.Join(opt.out, name)
	}
	if strings.HasSuffix(opt.out, string(filepath.Separator)) {
		return filepath.Join(opt.out, name)
	}
	return opt.out
}

func writeOne(users []string, bundles []Bundle, info map[string]AccountInfo, path string, out io.Writer) error {
	doc := Render(users, bundles, info, time.Now())
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintln(out, Good(fmt.Sprintf("\nwrote %s (%s bytes, %d repositories)",
		path, commas(int64(len(doc))), len(bundles))))
	return nil
}

// defaultOut puts the bundle in the user's Downloads folder, which is where a
// file you are about to open, read, or drag into something else belongs. The
// working directory is where the tool happens to be run from, which is rarely
// the same place. It falls back to the working directory when there is no home
// directory or no Downloads folder in it.
func defaultOut(users []string) string {
	name := strings.Join(users, "-") + "-omnibus.md"
	if len(users) > 3 {
		name = fmt.Sprintf("%s-and-%d-more-omnibus.md", users[0], len(users)-1)
	}
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
  repo-omnibus [flags] <github-user> [more-users...]

Examples:
  repo-omnibus hihipy
        Choose which repositories to collect, with arrow keys, then write
        ~/Downloads/hihipy-omnibus.md

  repo-omnibus -all hihipy
        Take everything without asking

  repo-omnibus -dry-run torvalds
        Report what it would cost and how big it would be, write nothing

  repo-omnibus -out ~/Desktop/charm.md charmbracelet
        Works on organizations, and -out chooses where the file lands

  repo-omnibus -exclude notes -exclude scratch hihipy
        Leave named repositories out

  repo-omnibus hihipy charmbracelet
        Two accounts, asked about one at a time, written to one file each

  repo-omnibus -merge hihipy charmbracelet
        The same two accounts in one combined file, with the owner on every
        repository heading since two accounts can hold the same name

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
                               one file per account, unless -out says
                               otherwise. -out FILE names the file, -out DIR
                               names the folder
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

// pickPerAccount asks about one account at a time. A single list mixing three
// people's repositories hides who owns what, and the names alone do not say.
func pickPerAccount(keep []Repo, opt options, out io.Writer) ([]Repo, error) {
	groups := groupByOwner(keep, opt.users)

	in := io.Reader(os.Stdin)
	if opt.plain {
		in = struct{ io.Reader }{os.Stdin} // hides os.Stdin, so Pick types
	}

	var chosen []Repo
	for _, g := range groups {
		if len(groups) > 1 {
			fmt.Fprintln(out, Bold(fmt.Sprintf("\n%s: %d repositories", g.owner, len(g.repos))))
		}
		// A short list can be short because the account is small or because
		// most of it was filtered. Those look identical without this line.
		if lines := filteredLines(opt.dropped[g.owner], opt.verbose); len(lines) > 0 {
			fmt.Fprintln(out, Notice("  not shown:"))
			for _, l := range lines {
				fmt.Fprintln(out, Notice("    "+l))
			}
		}
		picked, err := Pick(g.repos, in, out)
		if err != nil {
			return nil, err
		}
		if picked == nil {
			fmt.Fprintf(out, "%s: skipped\n", g.owner)
			continue
		}
		chosen = append(chosen, picked...)
	}
	return chosen, nil
}

// accountInfo records how many repositories each account holds and which of
// them a filter removed, so the finished document can say what is missing.
func accountInfo(repos []Repo, dropped map[string]map[string][]string) map[string]AccountInfo {
	info := map[string]AccountInfo{}

	byName := map[string]Repo{}
	for _, r := range repos {
		owner := strings.ToLower(ownerOf(r))
		entry := info[owner]
		entry.Total++
		info[owner] = entry
		byName[strings.ToLower(r.FullName)] = r
	}

	for owner, reasons := range dropped {
		key := strings.ToLower(owner)
		entry := info[key]
		for reason, names := range reasons {
			for _, name := range names {
				// The size reason carries the size in the name; the repository
				// itself is the part before the bracket.
				bare := name
				if i := strings.Index(bare, " ("); i > 0 {
					bare = bare[:i]
				}
				entry.Left = append(entry.Left, LeftOut{
					Name:   bare,
					Reason: plainReason(reason),
					URL:    fmt.Sprintf("https://github.com/%s/%s", owner, bare),
				})
			}
		}
		info[key] = entry
	}
	return info
}

// noteUnpicked records the repositories that were offered and not chosen, which
// is a different thing from one a filter removed.
func noteUnpicked(info map[string]AccountInfo, offered, chosen []Repo) {
	taken := map[string]bool{}
	for _, r := range chosen {
		taken[r.FullName] = true
	}
	for _, r := range offered {
		if taken[r.FullName] {
			continue
		}
		owner := strings.ToLower(ownerOf(r))
		entry := info[owner]
		entry.Left = append(entry.Left, LeftOut{
			Name:   r.Name,
			Reason: "not selected",
			URL:    r.HTMLURL,
		})
		info[owner] = entry
	}
}

// plainReason turns an internal reason into something a reader recognises.
func plainReason(reason string) string {
	switch {
	case reason == "fork":
		return "a fork of someone else's project"
	case reason == "archived":
		return "archived"
	case reason == "empty":
		return "empty"
	case reason == "excluded by name":
		return "excluded with -exclude"
	case strings.HasPrefix(reason, "over "):
		return "too large (" + strings.TrimSuffix(
			strings.TrimPrefix(reason, "over "), " (raise with -max-repo-kb)") + " limit)"
	}
	return reason
}

// filteredLines describes what an account lost, naming the repositories rather
// than only counting them, since "1 too large" does not say which one. Long
// lists are cut, unless -verbose asks for all of them.
func filteredLines(reasons map[string][]string, verbose bool) []string {
	if len(reasons) == 0 {
		return nil
	}

	type kind struct{ key, phrase, flag string }
	kinds := []kind{
		{"fork", "forks", "-include-forks"},
		{"archived", "archived", "-include-archived"},
		{"empty", "empty", ""},
		{"excluded by name", "excluded by name", ""},
	}
	for reason := range reasons {
		if strings.HasPrefix(reason, "over ") {
			kinds = append(kinds, kind{reason, "too large", "-max-repo-kb"})
		}
	}

	var lines []string
	for _, k := range kinds {
		names := reasons[k.key]
		if len(names) == 0 {
			continue
		}
		shown := names
		suffix := ""
		if !verbose && len(shown) > 4 {
			shown = shown[:3]
			suffix = fmt.Sprintf(" and %d more", len(names)-3)
		}
		line := fmt.Sprintf("%d %s: %s%s", len(names), k.phrase, strings.Join(shown, ", "), suffix)
		if k.flag != "" {
			line += "  (" + k.flag + ")"
		}
		lines = append(lines, line)
	}
	return lines
}

// reportEmptyAccounts names any account that had repositories but lost all of
// them to the filters. Without this an account you asked for simply produces no
// file, and nothing says why.
func reportEmptyAccounts(users []string, keep []Repo, dropped map[string]map[string][]string, out io.Writer) {
	kept := map[string]bool{}
	for _, r := range keep {
		kept[strings.ToLower(ownerOf(r))] = true
	}

	for _, u := range users {
		if kept[strings.ToLower(u)] {
			continue
		}

		// Say what happened to this account specifically, rather than listing
		// every flag and leaving the reader to work out which one applies.
		var reasons map[string][]string
		for owner, counts := range dropped {
			if strings.EqualFold(owner, u) {
				reasons = counts
			}
		}
		if len(reasons) == 0 {
			fmt.Fprintln(out, Notice(fmt.Sprintf(
				"%s: no public repositories, so no file was written", u)))
			continue
		}

		fmt.Fprintln(out, Notice(fmt.Sprintf(
			"%s: nothing to collect, so no file was written", u)))
		fmt.Fprintln(out, Notice("  every repository was filtered out:"))
		for _, l := range filteredLines(reasons, true) {
			fmt.Fprintln(out, Notice("    "+l))
		}
	}
}

// selectRepos applies the exclusions and reports what it dropped. An account
// with hundreds of forks would bury the useful output under one line each, so
// reasons are counted and only small groups are named.
func selectRepos(repos []Repo, opt options, out io.Writer) ([]Repo, map[string]map[string][]string) {
	excluded := make(map[string]bool, len(opt.exclude))
	for _, name := range opt.exclude {
		excluded[name] = true
	}

	var keep []Repo
	dropped := map[string][]string{}
	byOwner := map[string]map[string][]string{}
	var order []string
	drop := func(reason, name, owner, bare string) {
		if _, seen := dropped[reason]; !seen {
			order = append(order, reason)
		}
		dropped[reason] = append(dropped[reason], name)
		if byOwner[owner] == nil {
			byOwner[owner] = map[string][]string{}
		}
		// Inside an account's own block the owner is already known, so the
		// bare name reads better than the qualified one.
		byOwner[owner][reason] = append(byOwner[owner][reason], bare)
	}

	for _, r := range repos {
		owner := ownerOf(r)
		// With several accounts in one run, a bare repository name does not
		// say whose it is, and the reader guesses wrong.
		label := r.Name
		if len(opt.users) > 1 {
			label = r.FullName
		}
		switch {
		case excluded[r.Name]:
			drop("excluded by name", label, owner, r.Name)
		case r.Fork && !opt.includeForks:
			drop("fork", label, owner, r.Name)
		case r.Archived && !opt.includeArchived:
			drop("archived", label, owner, r.Name)
		case r.Size == 0:
			drop("empty", label, owner, r.Name)
		case opt.maxRepoKB > 0 && r.Size > opt.maxRepoKB:
			drop(fmt.Sprintf("over %s (raise with -max-repo-kb)",
				humanBytes(int64(opt.maxRepoKB)*1024)),
				fmt.Sprintf("%s (%s)", label, humanBytes(int64(r.Size)*1024)), owner,
				fmt.Sprintf("%s (%s)", r.Name, humanBytes(int64(r.Size)*1024)))
		default:
			keep = append(keep, r)
		}
	}

	for _, reason := range order {
		names := dropped[reason]
		line := fmt.Sprintf("skipped %d %s: %s", len(names), reason, strings.Join(names, ", "))
		if !opt.verbose && len(names) > 4 {
			line = fmt.Sprintf("skipped %d %s: %s and %d more",
				len(names), reason, strings.Join(names[:3], ", "), len(names)-3)
		}
		fmt.Fprintln(out, Quiet(line))
	}
	return keep, byOwner
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
