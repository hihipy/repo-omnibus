package omnibus

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// SkipStat is one reason files were left out, with how many and how much.
type SkipStat struct {
	Reason string
	Count  int
	Bytes  int64
}

// SkipEntry is one skipped file, carrying the repository it came from so it can
// be linked and found again.
type SkipEntry struct {
	Repo   Repo
	Path   string
	Reason string
	Detail string
	Size   int64
}

// SkipStats groups every skipped file by reason, largest group first. Ties
// break on total size, so the heaviest reason leads.
func SkipStats(bundles []Bundle) []SkipStat {
	byReason := map[string]*SkipStat{}
	for _, b := range bundles {
		for _, s := range b.Skipped {
			stat, ok := byReason[s.Reason]
			if !ok {
				stat = &SkipStat{Reason: s.Reason}
				byReason[s.Reason] = stat
			}
			stat.Count++
			stat.Bytes += s.Size
		}
	}

	out := make([]SkipStat, 0, len(byReason))
	for _, s := range byReason {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

// SkipEntries flattens every skipped file across every repository, heaviest
// first, since size is what decides whether a file is worth fetching by hand.
func SkipEntries(bundles []Bundle) []SkipEntry {
	var out []SkipEntry
	for _, b := range bundles {
		for _, s := range b.Skipped {
			out = append(out, SkipEntry{Repo: b.Repo, Path: s.Path,
				Reason: s.Reason, Detail: s.Detail, Size: s.Size})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Repo.Name+out[i].Path < out[j].Repo.Name+out[j].Path
	})
	return out
}

// blobURL points at one file on GitHub, which is where a reader has to go for
// anything Markdown cannot carry.
func blobURL(r Repo, path string) string {
	branch := r.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	return fmt.Sprintf("%s/blob/%s/%s", r.HTMLURL, branch, path)
}

// humanBytes renders a size the way a person reads it, since raw byte counts
// stop being meaningful past a few thousand.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// TypeTotal is one file type in the finished bundle, with what it contributes.
type TypeTotal struct {
	Ext   string
	Files int
	Chars int
}

// TypeTotals reports what the bundle is made of, heaviest first. Unlike the
// picker's histogram this counts everything that made it in, including READMEs,
// because here the question is what a reader or a model will actually be given.
func TypeTotals(bundles []Bundle) []TypeTotal {
	byExt := map[string]*TypeTotal{}
	for _, b := range bundles {
		for _, f := range b.Files {
			ext := strings.ToLower(path.Ext(f.Path))
			if ext == "" {
				ext = path.Base(f.Path) // Dockerfile, Makefile, and the like
			}
			t, ok := byExt[ext]
			if !ok {
				t = &TypeTotal{Ext: ext}
				byExt[ext] = t
			}
			t.Files++
			t.Chars += len(f.Text)
		}
	}

	out := make([]TypeTotal, 0, len(byExt))
	for _, t := range byExt {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Chars != out[j].Chars {
			return out[i].Chars > out[j].Chars
		}
		return out[i].Ext < out[j].Ext
	})
	return out
}

// ByWeight orders the repositories by how much of the bundle they occupy, which
// is what decides whether a model spends its context on them.
func ByWeight(bundles []Bundle) []Bundle {
	out := make([]Bundle, len(bundles))
	copy(out, bundles)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Chars() != out[j].Chars() {
			return out[i].Chars() > out[j].Chars()
		}
		return out[i].Repo.Name < out[j].Repo.Name
	})
	return out
}

// TerminalSummary is what the run prints when it finishes: the totals, what the
// bundle is made of, which repositories dominate it, and what was left out.
//
// maxList bounds each list, because a run over a big account can skip hundreds
// of files and the point is to be readable at a glance.
func TerminalSummary(bundles []Bundle, maxList int) string {
	files, chars := 0, 0
	for _, b := range bundles {
		files += len(b.Files)
		chars += b.Chars()
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "\n%d repositories, %s files, %s characters, roughly %s tokens\n",
		len(bundles), commas(int64(files)), commas(int64(chars)), commas(int64(chars/4)))

	if types := TypeTotals(bundles); len(types) > 0 && chars > 0 {
		shown := types
		if maxList > 0 && len(shown) > maxList {
			shown = shown[:maxList]
		}
		sb.WriteString("\nWhat it is made of:\n")
		for _, t := range shown {
			fmt.Fprintf(&sb, "  %4d %-8s %10s  %4.1f%%\n",
				t.Files, t.Ext, humanBytes(int64(t.Chars)),
				100*float64(t.Chars)/float64(chars))
		}
		if len(types) > len(shown) {
			var restFiles, restChars int
			for _, t := range types[len(shown):] {
				restFiles += t.Files
				restChars += t.Chars
			}
			fmt.Fprintf(&sb, "  %4d %-8s %10s  %4.1f%%\n", restFiles,
				fmt.Sprintf("+%d more", len(types)-len(shown)),
				humanBytes(int64(restChars)), 100*float64(restChars)/float64(chars))
		}
	}

	if len(bundles) > 1 && chars > 0 {
		heavy := ByWeight(bundles)
		shown := heavy
		if maxList > 0 && len(shown) > maxList {
			shown = shown[:maxList]
		}
		sb.WriteString("\nWhere the context goes:\n")
		for _, b := range shown {
			fmt.Fprintf(&sb, "  %10s  %4.1f%%  %-40s %3d files\n",
				humanBytes(int64(b.Chars())), 100*float64(b.Chars())/float64(chars),
				b.Repo.Name, len(b.Files))
		}
		if len(heavy) > len(shown) {
			fmt.Fprintf(&sb, "  and %d smaller\n", len(heavy)-len(shown))
		}
	}

	stats := SkipStats(bundles)
	if len(stats) == 0 {
		sb.WriteString("\nNo files were left out.\n")
		return sb.String()
	}

	total, bytes := 0, int64(0)
	for _, s := range stats {
		total += s.Count
		bytes += s.Bytes
	}
	fmt.Fprintf(&sb, "\nNot included: %d files, %s\n", total, humanBytes(bytes))

	width := 0
	for _, s := range stats {
		if len(s.Reason) > width {
			width = len(s.Reason)
		}
	}
	for _, s := range stats {
		fmt.Fprintf(&sb, "  %4d  %-*s  %10s\n", s.Count, width,
			strings.ReplaceAll(s.Reason, "`", ""), humanBytes(s.Bytes))
	}

	// Licenses are dropped on purpose and nobody wants to fetch 19 copies, so
	// they stay in the counts above but out of the download list.
	var entries []SkipEntry
	for _, e := range SkipEntries(bundles) {
		if !strings.Contains(e.Reason, "license text") {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return sb.String()
	}
	shown := entries
	if maxList > 0 && len(shown) > maxList {
		shown = shown[:maxList]
	}
	sb.WriteString("\nDownload these from GitHub if you need them:\n")
	for _, e := range shown {
		fmt.Fprintf(&sb, "  %10s  %s/%s\n", humanBytes(e.Size), e.Repo.Name, e.Path)
	}
	if len(entries) > len(shown) {
		fmt.Fprintf(&sb, "  and %d more, all listed under \"Not Included\" in the file\n",
			len(entries)-len(shown))
	}
	return sb.String()
}
