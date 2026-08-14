package omnibus

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// langByExt maps a file extension to the fence tag that highlights it.
var langByExt = map[string]string{
	".py": "python", ".r": "r", ".rmd": "rmarkdown", ".qmd": "quarto", ".ipynb": "json",
	".sql": "sql", ".sh": "bash", ".zsh": "zsh", ".bash": "bash", ".fish": "fish",
	".js": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".ts": "typescript", ".tsx": "tsx", ".jsx": "jsx", ".json": "json", ".jsonc": "json",
	".html": "html", ".htm": "html", ".css": "css", ".scss": "scss", ".sass": "sass",
	".yml": "yaml", ".yaml": "yaml", ".toml": "toml", ".ini": "ini", ".cfg": "ini",
	".md": "markdown", ".markdown": "markdown", ".txt": "text", ".csv": "csv",
	".tsv": "tsv", ".tex": "latex", ".sty": "latex", ".cls": "latex", ".bib": "bibtex",
	".bas": "vb", ".frm": "vb", ".vba": "vb", ".vb": "vb",
	".cs": "csharp", ".csx": "csharp", ".dax": "dax", ".m": "powerquery", ".pq": "powerquery",
	".xml": "xml", ".svg": "xml", ".plist": "xml", ".rb": "ruby", ".go": "go",
	".rs": "rust", ".java": "java", ".c": "c", ".h": "c", ".cpp": "cpp", ".hpp": "cpp",
	".swift": "swift", ".lua": "lua", ".pl": "perl", ".ps1": "powershell",
}

// langByName covers files whose name, not extension, identifies the syntax.
var langByName = map[string]string{
	"Dockerfile": "dockerfile", "Makefile": "makefile", ".gitignore": "gitignore",
	".gitattributes": "gitattributes", ".editorconfig": "ini", ".Rprofile": "r",
	"NAMESPACE": "r", "DESCRIPTION": "dcf",
}

func langFor(rel string) string {
	base := path.Base(rel)
	if l, ok := langByName[base]; ok {
		return l
	}
	if l, ok := langByExt[strings.ToLower(path.Ext(base))]; ok {
		return l
	}
	return "text"
}

// fenceFor returns a fence longer than any backtick run in the text, so a file
// containing its own code fence cannot break out of the block.
func fenceFor(text string) string {
	longest, run := 0, 0
	for _, ch := range text {
		if ch == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	if longest < 3 {
		longest = 3
	} else {
		longest++
	}
	return strings.Repeat("`", longest)
}

// anchor reproduces GitHub's heading-to-fragment rule for repository names.
func anchor(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, ".", "")
	return strings.ReplaceAll(s, " ", "-")
}

func commas(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

func shortDate(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// escapePipe keeps a description with a pipe in it from splitting a table cell.
func escapePipe(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

// extRow counts every file the repository tracks, including the ones left out
// of the bundle, which is free here because the tarball is already open.
func extRow(b Bundle) string {
	var paths []string
	for _, f := range b.Files {
		paths = append(paths, f.Path)
	}
	for _, s := range b.Skipped {
		paths = append(paths, s.Path)
	}
	hist := ExtHistogram(paths, 12)
	parts := make([]string, 0, len(hist))
	for _, e := range hist {
		parts = append(parts, fmt.Sprintf("%d x `%s`", e.Count, e.Ext))
	}
	return strings.Join(parts, ", ")
}

// primaryOnly reports the primary language when no breakdown was fetched, so
// the table never carries the same fact twice.
func primaryOnly(r Repo) string {
	if len(r.Langs) > 0 {
		return ""
	}
	return r.Language
}

// repoFacts is the metadata table under a repository heading. Rows with no
// value are dropped rather than shown empty.
func repoFacts(b Bundle) []string {
	r := b.Repo
	license := ""
	if r.License != nil && r.License.SPDXID != "" && r.License.SPDXID != "NOASSERTION" {
		license = r.License.SPDXID
	}
	topics := ""
	if len(r.Topics) > 0 {
		quoted := make([]string, len(r.Topics))
		for i, t := range r.Topics {
			quoted[i] = "`" + t + "`"
		}
		topics = strings.Join(quoted, ", ")
	}
	homepage := ""
	if r.Homepage != "" {
		homepage = "<" + r.Homepage + ">"
	}
	skipped := ""
	if len(b.Skipped) > 0 {
		skipped = commas(int64(len(b.Skipped)))
	}

	rows := [][2]string{
		{"Repository", fmt.Sprintf("[%s](%s)", r.FullName, r.HTMLURL)},
		{"Homepage", homepage},
		{"File types", extRow(b)},
		{"Languages", strings.Join(r.Langs, ", ")},
		{"Primary language", primaryOnly(r)},
		{"Topics", topics},
		{"License", license},
		{"Stars", fmt.Sprintf("%d", r.Stars)},
		{"Created", shortDate(r.CreatedAt)},
		{"Last push", shortDate(r.PushedAt)},
		{"Default branch", "`" + r.DefaultBranch + "`"},
		{"Files included", fmt.Sprintf("%s (%s characters)",
			commas(int64(len(b.Files))), commas(int64(b.Chars())))},
		{"Files skipped", skipped},
	}

	out := []string{"| Field | Value |", "| --- | --- |"}
	for _, row := range rows {
		if row[1] != "" {
			out = append(out, fmt.Sprintf("| %s | %s |", row[0], row[1]))
		}
	}
	return out
}

// Render assembles the whole document: a lead paragraph, a contents table, then
// each repository as an H1 with its metadata and every file it tracks.
func Render(user string, bundles []Bundle, now time.Time) string {
	totalFiles, totalChars := 0, 0
	for _, b := range bundles {
		totalFiles += len(b.Files)
		totalChars += b.Chars()
	}

	var out []string
	add := func(lines ...string) { out = append(out, lines...) }

	add(fmt.Sprintf("**RepoOmnibus** collected every public repository owned by "+
		"[%s](https://github.com/%s) on %s.",
		user, user, now.UTC().Format("2006-01-02 15:04 UTC")))
	add("")
	add(fmt.Sprintf("%d repositories, %s files, %s characters, roughly %s tokens.",
		len(bundles), commas(int64(totalFiles)), commas(int64(totalChars)),
		commas(int64(totalChars/4))))
	add("")
	add("Each repository below opens with a metadata table, then every text file " +
		"it tracks, in full. Binary files, oversized files, and files that are not " +
		"valid UTF-8 are listed with a reason at the end of their repository.")
	add("")
	add("**Rights.** This file contains copies of source files whose copyright " +
		"belongs to their authors. Public does not mean unlicensed: a repository " +
		"with no licence file is covered by ordinary copyright, and one with a " +
		"licence is covered by its terms, including any requirement to keep " +
		"notices attached. Licence files are omitted by default, so this bundle " +
		"is not a licensed distribution of anything in it. Treat it as a private " +
		"working copy, and go to each repository for terms before reusing, " +
		"publishing, or redistributing any part of it.")
	add("", "## Contents", "")
	add("| Repository | Language | Description | Files |", "| --- | --- | --- | --- |")
	for _, b := range bundles {
		add(fmt.Sprintf("| [%s](#%s) | %s | %s | %s |",
			b.Repo.Name, anchor(b.Repo.Name), b.Repo.Language,
			escapePipe(b.Repo.Description), commas(int64(len(b.Files)))))
	}
	add("")

	if entries := SkipEntries(bundles); len(entries) > 0 {
		stats := SkipStats(bundles)
		var bytes int64
		for _, s := range stats {
			bytes += s.Bytes
		}
		add("## Not Included", "")
		add(fmt.Sprintf("%d files could not be carried as text, %s in total. "+
			"Spreadsheets, images, and other binaries have to be downloaded from "+
			"GitHub directly. Each link below goes straight to the file.",
			len(entries), humanBytes(bytes)))
		add("", "| File | Repository | Reason | Size |", "| --- | --- | --- | --- |")
		for _, e := range entries {
			reason := e.Reason
			if e.Detail != "" {
				reason += ": " + e.Detail
			}
			add(fmt.Sprintf("| [`%s`](%s) | [%s](#%s) | %s | %s |",
				e.Path, blobURL(e.Repo, e.Path), e.Repo.Name, anchor(e.Repo.Name),
				reason, humanBytes(e.Size)))
		}
		add("")
	}

	for _, b := range bundles {
		add("---", "", "# "+b.Repo.Name, "")
		if b.Repo.Description != "" {
			add(b.Repo.Description, "")
		}
		add(repoFacts(b)...)
		add("")

		branch := b.Repo.DefaultBranch
		if branch == "" {
			branch = "main"
		}
		for _, f := range b.Files {
			add(fmt.Sprintf("## [`%s`](%s/blob/%s/%s)", f.Path, b.Repo.HTMLURL, branch, f.Path), "")
			if f.Note != "" {
				add("*"+f.Note+"*", "")
			}
			if f.Note != "" && strings.Contains(f.Note, "extracted") {
				// Extracted text is already Markdown, so it is not fenced.
				add(strings.TrimRight(f.Text, "\n"), "")
				continue
			}
			fence := fenceFor(f.Text)
			add(fence+langFor(f.Path), strings.TrimRight(f.Text, "\n"), fence, "")
		}

		if len(b.Skipped) > 0 {
			add("## Skipped in "+b.Repo.Name, "", "| File | Reason | Size |", "| --- | --- | --- |")
			for _, s := range b.Skipped {
				reason := s.Reason
				if s.Detail != "" {
					reason += ": " + s.Detail
				}
				add(fmt.Sprintf("| `%s` | %s | %s |", s.Path, reason, commas(s.Size)))
			}
			add("")
		}
	}

	return strings.Join(out, "\n")
}
