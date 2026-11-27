// Command typecheck type-checks every package of this module from source, test files included, and prints
// every error it finds.
//
// Unlike `go build ./...`, it does not stop at the first broken package: packages that import a broken package
// are still checked, so one run shows the whole picture after a large rename. Expect some follow-on errors
// (an undefined name in one package can cause errors where it is used).
//
//	go run ./tools/rebrand/typecheck [-tags full] [-tests=false] [-o errors.txt] [patterns...]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

func main() {
	tags := flag.String("tags", "", "comma-separated build tags")
	tests := flag.Bool("tests", true, "also check test files")
	out := flag.String("o", "", "write the error list to this file as well")
	flag.Parse()
	patterns := flag.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports |
			packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedTypesSizes,
		Tests: *tests,
	}
	if *tags != "" {
		cfg.BuildFlags = []string{"-tags=" + *tags}
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(2)
	}

	wd, _ := os.Getwd()
	seen := map[string]bool{}
	var lines []string
	broken := map[string]bool{}
	for _, p := range pkgs {
		for _, e := range p.Errors {
			s := e.Error()
			if rel, ok := strings.CutPrefix(s, wd+string(filepath.Separator)); ok {
				s = rel
			}
			if !seen[s] {
				seen[s] = true
				lines = append(lines, s)
				broken[strings.TrimSuffix(strings.Fields(p.ID)[0], "_test")] = true
			}
		}
	}
	sort.Strings(lines)
	text := strings.Join(lines, "\n")
	if text != "" {
		text += "\n"
	}
	summary := fmt.Sprintf("typecheck: %d packages loaded, %d with errors, %d errors\n", len(pkgs), len(broken), len(lines))
	fmt.Print(text)
	fmt.Fprint(os.Stderr, summary)
	if *out != "" {
		if err := os.WriteFile(*out, []byte(text+summary), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(2)
		}
	}
}
