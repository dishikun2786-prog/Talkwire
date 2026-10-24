// Command redlines checks the red-line matrix and renders it.
//
//	redlines check   # matrix vs scenarios, and matrix.md is up to date
//	redlines write   # regenerate matrix.md
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"example.com/imkit/tools/conformance/harness"
	"example.com/imkit/tools/conformance/redlines"
)

func main() {
	if len(os.Args) < 2 || (os.Args[1] != "check" && os.Args[1] != "write") {
		fmt.Fprintln(os.Stderr, "usage: redlines check|write [-matrix file] [-out file] [-scenarios dir]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	matrixPath := fs.String("matrix", "../../contract/redlines/redlines.json", "red-line registry")
	outPath := fs.String("out", "../../contract/redlines/matrix.md", "rendered table")
	scenarioDir := fs.String("scenarios", "../../contract/protocol/scenarios", "scenario directory")
	_ = fs.Parse(os.Args[2:])

	m, err := redlines.Load(*matrixPath)
	if err != nil {
		fail(err)
	}
	scs, err := harness.LoadScenarios(*scenarioDir)
	if err != nil {
		fail(err)
	}
	problems := redlines.Check(m, scs)
	for _, p := range problems {
		fmt.Println("PROBLEM", p)
	}
	rendered := redlines.Render(m)

	switch cmd {
	case "write":
		if len(problems) > 0 {
			fmt.Printf("not written: %d problem(s)\n", len(problems))
			os.Exit(1)
		}
		if err := os.WriteFile(*outPath, rendered, 0o644); err != nil {
			fail(err)
		}
		fmt.Printf("wrote %s (%d red lines)\n", *outPath, len(m.Redlines))
	case "check":
		current, err := os.ReadFile(*outPath)
		if err != nil {
			fail(err)
		}
		if !bytes.Equal(current, rendered) {
			fmt.Printf("PROBLEM %s is out of date: run redlines write\n", *outPath)
			problems = append(problems, "stale")
		}
		if len(problems) > 0 {
			fmt.Printf("FAIL %d problem(s)\n", len(problems))
			os.Exit(1)
		}
		fmt.Printf("OK %d red lines consistent with %d scenarios; %s up to date\n", len(m.Redlines), len(scs), *outPath)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "redlines:", err)
	os.Exit(2)
}
