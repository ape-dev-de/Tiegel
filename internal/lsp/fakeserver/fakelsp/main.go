// Command fakelsp runs the deterministic fake language server of package
// fakeserver on stdin/stdout. Tests build it to exercise Tiegel's LSP
// integration end to end without a real PHP language server.
package main

import "github.com/ape-dev-de/tiegel/internal/lsp/fakeserver"

func main() { fakeserver.Main() }
