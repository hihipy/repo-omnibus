# RepoOmnibus

[![Link Check](https://github.com/hihipy/repo-omnibus/actions/workflows/links.yml/badge.svg)](https://github.com/hihipy/repo-omnibus/actions/workflows/links.yml)
[![License: CC BY-NC-SA 4.0](https://img.shields.io/badge/License-CC%20BY--NC--SA%204.0-lightgrey.svg)](https://creativecommons.org/licenses/by-nc-sa/4.0/)

**Built with**

[![GitHub Actions](https://img.shields.io/badge/GitHub%20Actions-2088FF?style=flat&logo=githubactions&logoColor=white)](https://github.com/features/actions)
[![Go](https://img.shields.io/badge/Go-00ADD8?style=flat&logo=go&logoColor=white)](https://go.dev)
[![Markdown](https://img.shields.io/badge/Markdown-000000?style=flat&logo=markdown&logoColor=white)](https://commonmark.org)

**Every repository someone owns, in one file.**

RepoOmnibus takes a GitHub username and produces a single document containing all of that person's public work: what each project is, how big it is, and the full text of every file worth reading. You can read the result yourself, or paste it into ChatGPT or Claude and ask questions about the whole body of work at once.

---

## What problem this solves

Say you want to understand what someone has built, or ask an AI assistant a question that spans several of your own projects. Today that means opening repository after repository and copying files by hand.

Tools exist that turn **one** repository into one file. None of them take a whole account.

There is a second problem. Code projects are full of text nobody reads: dependency lists thousands of lines long, machine-written data files, the same license repeated in every project. It is all technically readable, so those tools include it, and it can end up being most of what you paste. On one real account it was two thirds of the file.

RepoOmnibus does both jobs. It collects the whole account, and it leaves out the noise.

---

## Installing it

You need two things, and both are free.

### Step 1: install Go

Go is the programming language this tool is written in. Installing it gives you the command that turns the source code into a working program.

**macOS.** If you have [Homebrew](https://brew.sh), open **Terminal** and run:

```bash
brew install go
```

If you do not have Homebrew, download the macOS installer from [go.dev/dl](https://go.dev/dl/) and double-click it.

**Windows.** Download the Windows installer from [go.dev/dl](https://go.dev/dl/) and run it. Then use **PowerShell** wherever this page says Terminal.

**Linux.** Use your package manager, for example `sudo apt install golang-go`, or download from [go.dev/dl](https://go.dev/dl/).

To check it worked, run this. It should print a version number.

```bash
go version
```

### Step 2: download and build this tool

In Terminal, run these three lines one at a time:

```bash
git clone https://github.com/hihipy/repo-omnibus.git
cd repo-omnibus
go build -o repo-omnibus ./cmd/repo-omnibus
```

The first line copies this project to your computer. The second moves into the folder it created. The third builds the program, which takes a few seconds and prints nothing when it works.

You now have a file called `repo-omnibus` in that folder. That is the whole tool: one file, nothing installed anywhere else, nothing running in the background.

---

## Using it

Point it at any GitHub username:

```bash
./repo-omnibus hihipy
```

It prints what it is doing as it goes, then writes a file called `hihipy-omnibus.md` in the same folder. Open that file in any text editor, or in a Markdown reader like [Obsidian](https://obsidian.md) or [Typora](https://typora.io), or drag it into ChatGPT or Claude and start asking questions.

To see what a run would involve before doing it:

```bash
./repo-omnibus -dry-run hihipy
```

That reports how many repositories there are and roughly how big the result would be, and writes nothing.

To save the file somewhere else:

```bash
./repo-omnibus -out ~/Downloads/my-work.md hihipy
```

It works on organizations as well as people:

```bash
./repo-omnibus charmbracelet
```

---

## Picking which projects to include

Some accounts have one enormous repository that would dominate the file. Add `-pick` and it shows you the list first:

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

Everything starts ticked. Move with the arrow keys, press **space** to untick something, press **enter** when you are happy. The running total at the bottom updates as you go, so you can see the effect of dropping the big one. Press **q** to back out without doing anything.

---

## A note on GitHub's limits

GitHub lets anyone make 60 requests an hour without signing in. Each repository costs about one request, so accounts up to roughly 25 repositories work with no setup at all.

Above that, you need a token, which is a password-like string that tells GitHub who is asking. It raises the limit to 5,000 an hour. Getting one takes two minutes:

1. Go to [github.com/settings/tokens](https://github.com/settings/tokens)
2. Create a token, tick nothing, and copy it
3. In Terminal, run `export GITHUB_TOKEN=paste-it-here`

A token with no permissions ticked is enough, because this tool only ever reads public information. If you already use the [GitHub CLI](https://cli.github.com), you can skip all of that:

```bash
export GITHUB_TOKEN=$(gh auth token)
```

That line lasts until you close the Terminal window.

**Only public repositories are ever read.** A token raises how often you may ask; it does not widen what is visible. Anything marked private is discarded even if it somehow appeared.

---

## Before you point this at someone else's account

Reading a public repository is not the same as being allowed to copy it, and this tool makes copies.

**Public does not mean free to reuse.** A project with no license file is covered by ordinary copyright, which means nobody may copy or republish it. A project with a license is covered by that license, including any requirement to keep notices attached. Publishing code on GitHub lets other people view and fork it there, which is narrower than a right to redistribute a copy elsewhere. GitHub explains this in [licensing a repository](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/licensing-a-repository) and in its [Terms of Service](https://docs.github.com/en/site-policy/github-terms/github-terms-of-service).

**The file this produces is not a licensed distribution.** License files are left out by default, because the same text repeated forty times is wasted space. That is the right choice for reading and the wrong one for republishing. `-include-licenses` keeps them if you need the notices attached.

**In practice.** Treat the file as a private working copy. Keep it on your machine, or hand it to an AI assistant you are using yourself. Do not publish it, host it, or ship it inside a product. Before reusing any code you find in it, go to the project it came from and read the terms there.

**Terms of use.** This tool is provided as is, without warranty of any kind. You are responsible for how you use it and for complying with copyright law, with GitHub's terms, and with the license of every project you collect. Nothing here is legal advice, and the author is not a lawyer. If you are unsure whether something is allowed, ask someone who is.

Every file this produces carries a short version of that notice at the top, so it explains itself if it ever travels without this page.

---

## What the finished file looks like

It opens with a table of contents, then a table of anything that could not be included, each entry linked to the file on GitHub. After that comes each project: a summary table, then every file in full, README first.

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

Excel and Word files are read rather than skipped. A spreadsheet becomes a table per sheet, and a cell holding a formula shows the formula, so a project whose whole product is a spreadsheet is represented by more than its description.

---

## What gets left out, and why

Each run ends with a report of what it collected and what it dropped:

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

That "tokens" figure is roughly how much of an AI assistant's reading capacity the file would use, which is the number that decides whether it will fit.

Files are dropped when they are images or other non-text, too large, a license, a dependency list, marked by the tool that generated them, or sitting in a folder holding copied-in or built code. Everything else is judged by what it looks like inside, rather than by its name, because every account brings some format nobody thought to list:

| What it notices | Where the line sits | What that catches |
|---|---|---|
| One enormous line | over 5,000 characters | Compressed web files, drawings saved as text |
| Long lines throughout | averaging over 300 characters | Machine-written output |
| The same line over and over | under 35% different, files above 32 KB | Circuit board layouts, data tables |
| Almost no letters | under 25% letters, files above 32 KB | Coordinates and checksums |

For comparison, code a person wrote is about 70% letters with 80% of its lines different from each other. A circuit board file is 38% and 14%.

Nothing disappears quietly: every excluded file is counted in the report and listed with its reason in the file itself. `-include-generated` and `-include-licenses` bring them back.

---

## All the options

| Option | What it does |
|---|---|
| `-out PATH` | Where to save; defaults to `./<user>-omnibus.md` |
| `-pick` | Choose projects before collecting |
| `-dry-run` | Report what a run would involve, write nothing |
| `-exclude NAME` | Leave a project out; use it more than once for several |
| `-include-forks` | Include copies of other people's projects |
| `-include-archived` | Include projects marked as no longer maintained |
| `-include-licenses` | Keep license files |
| `-include-generated` | Keep dependency lists, copied-in code, and machine-written files |
| `-skip-office` | Link spreadsheets and documents rather than reading them |
| `-max-repo-kb N` | Skip projects bigger than this; `0` for no limit, default 51200 |
| `-max-file-bytes N` | Skip files bigger than this, default 524288 |
| `-no-file-types` | Skip the per-project file-type counts |
| `-plain` | Type a selection instead of using arrow keys |
| `-verbose` | Name every skipped project instead of counting them |
| `-ignore-budget` | Start even when GitHub's limit looks too small |

Running `./repo-omnibus -help` prints all of this in the terminal, with examples.

---

## Things it cannot do

**PDFs are linked, not read.** Their text is stored in a way that needs a dedicated library to decode, and a wrong answer would be worse than an honest link.

**Very small machine-written files slip through.** The checks need enough text to measure, so a tiny generated file gets in. It costs a few hundred characters.

**One project is held in memory at a time.** Very large projects are skipped rather than streamed, which is what the size limit is for. Some accounts have repositories of several gigabytes, and without the limit one of them would bring a laptop to a halt.

---

## For developers

Written in Go with no dependencies outside the standard library.

```
repo-omnibus/
├── cmd/repo-omnibus/
│   └── main.go            entry point
└── internal/omnibus/
    ├── github.go          API client, rate-limit budget, file-type histogram
    ├── cache.go           remembers file lists between runs
    ├── bundle.go          reads a repository archive, decides what to keep
    ├── triage.go          judges a file by its shape rather than its name
    ├── office.go          reads xlsx and docx without a dependency
    ├── markdown.go        renders the document
    ├── summary.go         renders the terminal report
    ├── pick.go            typed selection
    ├── pickui.go          arrow-key selection
    └── run.go             flags and orchestration
```

```bash
go test ./...
```

Each repository is fetched as a single archive rather than file by file, which is what keeps a whole account inside GitHub's hourly limit. File lists are cached under `~/Library/Caches/repo-omnibus/`, keyed on each project's last push, so a repeat run costs nothing until someone pushes.

---

## License

This project is licensed under [CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/).

- **Attribution.** Credit the original work.
- **NonCommercial.** No commercial use.
- **ShareAlike.** Derivatives must use the same license.
