# RepoOmnibus

[![Link Check](https://github.com/hihipy/repo-omnibus/actions/workflows/links.yml/badge.svg)](https://github.com/hihipy/repo-omnibus/actions/workflows/links.yml)
[![Go](https://github.com/hihipy/repo-omnibus/actions/workflows/go.yml/badge.svg)](https://github.com/hihipy/repo-omnibus/actions/workflows/go.yml)
[![License: CC BY-NC-SA 4.0](https://img.shields.io/badge/License-CC%20BY--NC--SA%204.0-lightgrey.svg)](https://creativecommons.org/licenses/by-nc-sa/4.0/)

**Built with**

[![GitHub Actions](https://img.shields.io/badge/GitHub%20Actions-2088FF?style=flat&logo=githubactions&logoColor=white)](https://github.com/features/actions)
[![Go](https://img.shields.io/badge/Go-00ADD8?style=flat&logo=go&logoColor=white)](https://go.dev)
[![Markdown](https://img.shields.io/badge/Markdown-000000?style=flat&logo=markdown&logoColor=white)](https://commonmark.org)

**Every repository you own, in one file.**

RepoOmnibus reads every public repository on a GitHub account and writes a single Markdown file containing all of it: each repository's description, its statistics, and the full text of every file worth reading. The result is meant to be read by a person or handed to a language model, and it works for either.

---

## Why this exists

Several tools turn one repository into a single file for an AI assistant. None of them take an account. If you want to show someone the shape of your work, or ask a model a question that spans four of your projects at once, you are left copying files by hand.

There is a second problem those tools share. A repository is full of text that nobody reads: lock files, minified bundles, CAD geometry, recorded test output, license boilerplate repeated in every project. It is all valid text, so it all gets included, and it can be most of what you end up pasting. On one real account it was two thirds.

RepoOmnibus does both jobs. It collects the account, and it leaves out the noise.

---

## Getting started

You need [Go 1.22 or newer](https://go.dev/dl/).

```bash
git clone https://github.com/hihipy/repo-omnibus.git
cd repo-omnibus
go build -o repo-omnibus ./cmd/repo-omnibus
```

Then point it at any account:

```bash
./repo-omnibus hihipy
```

That writes `hihipy-omnibus.md` in the current directory. To see what a run would cost before spending anything:

```bash
./repo-omnibus -dry-run hihipy
```

---

## Choosing repositories

`-pick` lists the account and waits. Everything starts selected, so pressing enter takes all of it.

```bash
./repo-omnibus -pick hihipy
```

```
> [x] 25live-cleaner        Turns raw 25Live Excel exports into a clean CSV   1x.py            37 KB
  [x] excel-vba-toolkit     A collection of Excel VBA macros                  10x.bas         116 KB
  [ ] hihipy.github.io      Personal Portfolio Website                        26x.html        29,920 KB
  [x] sql-x-ray             See the structure, not the data.                  8x.sql          285 KB

  19 of 21 selected, 4,790 KB
  arrows move, space toggles, a all, n none, enter confirms, q cancels
```

The size total updates as you go, which matters when one repository is a hundred times the size of the others. If the terminal cannot be driven, it falls back to typing numbers, names, or globs.

---

## Rate limits

GitHub allows 60 requests an hour without a token and 5,000 with one. A run costs one request per repository, plus one more for each repository whose file list is not already cached.

```bash
export GITHUB_TOKEN=$(gh auth token)
```

Anything above roughly 25 repositories needs a token. Only public repositories are ever read: the endpoint this uses returns public repositories to any caller, a token raises the limit rather than widening what is visible, and anything marked private is discarded regardless.

File lists are cached under `~/Library/Caches/repo-omnibus/`, keyed on each repository's last push, so a second run costs nothing until someone pushes.

---

## Before you point this at someone else's account

Reading a public repository is not the same as being allowed to copy it, and this tool makes copies.

**Public does not mean unlicensed.** A repository with no licence file is covered by ordinary copyright, which means no one may reproduce or redistribute it. A repository with a licence is covered by that licence, including any obligation to keep notices attached to copies. Publishing a repository grants other people the right to view and fork it on GitHub, which is narrower than a right to redistribute a copy of it somewhere else. See GitHub's own guidance on [licensing a repository](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/licensing-a-repository) and the [Terms of Service](https://docs.github.com/en/site-policy/github-terms/github-terms-of-service).

**A bundle is not a licensed distribution.** Licence files are left out by default, because the same text repeated forty times is noise in a context window. That is the right default for reading and the wrong one for redistribution. `-include-licenses` keeps them if you need the notices attached.

**What this means in practice.** Treat a bundle as a private working copy: keep it on your machine, or hand it to a model you are using yourself. Do not publish one, host one, or ship one inside a product. Before reusing any code you find in a bundle, go to the repository it came from and read its terms there.

**Terms of use of this tool.** It is provided as is, without warranty of any kind. You are responsible for how you use it and for complying with copyright law, with GitHub's terms, and with the licence of every repository you collect. Nothing here is legal advice, and the author is not a lawyer. If you are unsure whether a use is permitted, ask someone who is.

Every bundle carries a short version of this notice at the top, so the file explains itself when it travels without the README.

---

## What the file looks like

The bundle opens with a contents table, then a table of everything that could not be carried as text, each entry linked to the file on GitHub. After that comes each repository: a metadata table, then every file in full, with README first.

```markdown
## Contents

| Repository | Language | Description | Files |
| --- | --- | --- | --- |
| [sql-x-ray](#sql-x-ray) | PLSQL | See the structure, not the data. | 12 |

---

# sql-x-ray

| Field | Value |
| --- | --- |
| Repository | [hihipy/sql-x-ray](https://github.com/hihipy/sql-x-ray) |
| File types | 8 x `.sql`, 2 x `.md` |
| Stars | 4 |
| Last push | 2026-08-14 |

## [`scripts/postgres-xray.sql`](https://github.com/hihipy/sql-x-ray/blob/main/scripts/postgres-xray.sql)
```

Spreadsheets and Word documents are read rather than skipped. An `.xlsx` becomes a Markdown table per sheet, and a cell holding a formula shows the formula, so a repository whose whole product is a spreadsheet is represented by more than its README.

---

## What gets left out

The run ends with a summary of what it collected and what it dropped:

```
43 repositories, 4,532 files, 18,187,340 characters, roughly 4,546,835 tokens

What it is made of:
  2200 .go         11.1 MB  63.9%
   374 .yaml        3.6 MB  20.6%
   266 .md          727 KB   4.1%

Where the context goes:
      4.6 MB  26.6%  fantasy                                  481 files
      4.6 MB  26.5%  crush                                    744 files

Not included: 886 files, 29.5 MB
   365  machine-written                                    1.9 MB
   197  a binary file                                      6.5 MB
   111  marked as generated by the tool that wrote it      3.1 MB
    56  a serialized blob                                 12.8 MB
```

A file is dropped when it is binary, over the size limit, a license, a dependency lock file, marked generated by the tool that wrote it, or sitting in a vendored or built directory. Beyond that, files are judged by shape rather than by name, because every account brings a format no list anticipated:

| Signal | Threshold | What it catches |
|---|---|---|
| One very long line | over 5,000 characters | Minified bundles, source maps, rendered SVG |
| High mean line length | over 300 characters | Serialized output |
| Few unique lines | under 35%, above 32 KB | CAD geometry, model catalogs, generated schemas |
| Few letters | under 25%, above 32 KB | Coordinate and hash dumps |

For comparison, authored Go measures 70% letters with 80% of its lines unique. A KiCad schematic measures 38% and 14%.

Every exclusion is counted in the summary and listed with a reason in the file, so nothing disappears quietly. `-include-generated` and `-include-licenses` bring them back.

---

## Options

| Flag | What it does |
|---|---|
| `-out PATH` | Where to write; defaults to `./<user>-omnibus.md` |
| `-pick` | Choose repositories before collecting |
| `-dry-run` | Report cost and size, write nothing |
| `-exclude NAME` | Leave a repository out; repeatable |
| `-include-forks` | Include forks |
| `-include-archived` | Include archived repositories |
| `-include-licenses` | Keep LICENSE files |
| `-include-generated` | Keep lock files, vendored code, and machine-written files |
| `-skip-office` | Link spreadsheets and documents rather than reading them |
| `-max-repo-kb N` | Skip repositories larger than this; `0` for no limit, default 51200 |
| `-max-file-bytes N` | Skip files larger than this, default 524288 |
| `-no-file-types` | Skip the per-repository file-type counts |
| `-plain` | Type a selection instead of using arrow keys |
| `-verbose` | Name every skipped repository instead of counting them |
| `-ignore-budget` | Start even when the quota looks too small |

`repo-omnibus -help` prints all of this in the terminal.

---

## Project structure

```
repo-omnibus/
├── cmd/repo-omnibus/
│   └── main.go            entry point
└── internal/omnibus/
    ├── github.go          API client, rate-limit budget, file-type histogram
    ├── cache.go           remembers file lists between runs
    ├── bundle.go          reads a repository tarball, decides what to keep
    ├── triage.go          judges a file by its shape rather than its name
    ├── office.go          reads xlsx and docx without a dependency
    ├── markdown.go        renders the document
    ├── summary.go         renders the terminal report
    ├── pick.go            typed selection
    ├── pickui.go          arrow-key selection
    └── run.go             flags and orchestration
```

No dependencies outside the standard library. `go test ./...` runs the suite.

---

## Limitations

**PDFs are linked, not read.** Their text lives in compressed streams with font-level encodings, and a wrong answer is worse than an honest link.

**Very small generated files pass.** The shape rules need something to measure, so a thirty-byte stub gets through. It costs a few hundred bytes.

**One repository is held in memory at a time.** Repositories above the size limit are skipped rather than streamed, which is why the limit exists.

---

## License

This project is licensed under [CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/).

- **Attribution.** Credit the original work.
- **NonCommercial.** No commercial use.
- **ShareAlike.** Derivatives must use the same license.
