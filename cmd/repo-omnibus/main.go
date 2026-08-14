// Command repo-omnibus collects every public repository an account owns into
// one Markdown file, readable on its own and usable as context for a language
// model.
//
// Repositories come from the GitHub REST API. Each one is downloaded as a
// single tarball rather than file by file, which keeps a whole account inside
// the unauthenticated rate limit of 60 requests an hour: one listing request
// per page plus one per repository.
//
// Only public repositories are ever read. The listing endpoint returns public
// repositories only to an unauthenticated caller, and no token is sent.
//
// Usage:
//
//	repo-omnibus -dry-run hihipy
//	repo-omnibus hihipy
//	repo-omnibus -out ~/Downloads/hihipy.md -exclude notes hihipy
package main

import (
	"fmt"
	"os"

	"github.com/hihipy/repo-omnibus/internal/omnibus"
)

func main() {
	if err := omnibus.Run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
