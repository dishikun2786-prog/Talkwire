// Command redlines checks the red-line matrix and renders it.
//
//	redlines check   # matrix vs scenarios and server/Android/web tests, and matrix.md is up to date
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
		fmt.Fprintln(os.Stderr, "usage: redlines check|write [-matrix file] [-out file] [-scenarios dir] [-tests dir] [-android dir] [-web dir]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	matrixPath := fs.String("matrix", "../../contract/redlines/redlines.json", "red-line registry")
	outPath := fs.String("out", "../../contract/redlines/matrix.md", "rendered table")
	scenarioDir := fs.String("scenarios", "../../contract/protocol/scenarios", "scenario directory")
	testDir := fs.String("tests", "../../server", "server module whose tests mark cases written")
	androidDir := fs.String("android", "../../android", "Android project whose unit tests mark cases written")
	webDir := fs.String("web", "../../web", "web workspace whose Vitest and Playwright cases mark cases written")
	_ = fs.Parse(os.Args[2:])

	m, err := redlines.Load(*matrixPath)
	if err != nil {
		fail(err)
	}
	scs, err := harness.LoadScenarios(*scenarioDir)
	if err != nil {
		fail(err)
	}
	serverTests, err := redlines.WrittenTests(*testDir)
	if err != nil {
		fail(err)
	}
	androidTests, err := redlines.AndroidTests(*androidDir)
	if err != nil {
		fail(err)
	}
	webTests, err := redlines.WebTests(*webDir)
	if err != nil {
		fail(err)
	}
	written := redlines.Written{"server": serverTests, "android": androidTests, "web": webTests}
	problems := append(redlines.Check(m, scs), redlines.CheckWritten(m, written)...)
	for _, p := range problems {
		fmt.Println("PROBLEM", p)
	}
	rendered := redlines.Render(m, written)

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
