// Command replay plays the server's side of one golden transcript to a client
// under test, then exits 0 if the client followed the script.
//
//	replay -scenario core/p2p -user A
//	replay -scenario core/p2p -user A -faults '[{"kind":"disconnect","step":9}]'
//	replay -scenario module/call -conns b -order last -report out.json
//
// The client connects to ws://<addr>/ (any path) and logs in as the subject
// user; tokens are not checked. -bindings prints the concrete value of every
// placeholder (uids, topics, captured ids) so the client can be pointed at
// the right peers.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dishikun2786-prog/Talkwire/tools/conformance/harness"
	"github.com/dishikun2786-prog/Talkwire/tools/conformance/replay"
)

func main() {
	scenarios := flag.String("scenarios", "../../contract/protocol/scenarios", "scenario directory")
	transcripts := flag.String("transcripts", "../../contract/protocol/transcripts", "golden transcript directory")
	name := flag.String("scenario", "", "scenario name, e.g. core/p2p (required)")
	user := flag.String("user", "", "play as this scenario user (all its connections)")
	conns := flag.String("conns", "", "or: comma-separated connection names to play")
	addr := flag.String("addr", "127.0.0.1:17062", "listen address")
	faults := flag.String("faults", "", "faults as JSON (inline array, or @file)")
	order := flag.String("order", "first", "where the reply goes among a step's frames: first|last")
	skipOptional := flag.Bool("skip-optional", false, "leave out frames the reference server sends only sometimes")
	allowExtra := flag.Bool("allow-extra", false, "skip unscripted client frames (logged, not answered) instead of failing")
	timeout := flag.Duration("timeout", 10*time.Second, "how long to wait for the client at each step")
	timeScale := flag.Float64("timescale", 1, "multiplier for scripted waits (0 skips them)")
	report := flag.String("report", "", "write the report as JSON to this file")
	bindings := flag.Bool("bindings", false, "print placeholder values and exit")
	flag.Parse()

	if *name == "" {
		fail("-scenario is required")
	}
	sc, tr := load(*scenarios, *transcripts, *name)
	b := replay.NewBindings(sc, tr)
	if *bindings {
		printBindings(b)
		return
	}

	opt := replay.Options{
		Order: *order, SkipOptional: *skipOptional, AllowExtra: *allowExtra,
		Timeout: *timeout, TimeScale: *timeScale,
	}
	switch {
	case *conns != "":
		opt.Conns = strings.Split(*conns, ",")
	case *user != "":
		opt.Conns = replay.ConnsForUser(sc, *user)
		if len(opt.Conns) == 0 {
			fail("user %s never logs in in %s", *user, sc.Name)
		}
	default:
		fail("give -user or -conns")
	}
	if *faults != "" {
		raw := []byte(*faults)
		if strings.HasPrefix(*faults, "@") {
			var err error
			if raw, err = os.ReadFile(strings.TrimPrefix(*faults, "@")); err != nil {
				fail("%v", err)
			}
		}
		if err := json.Unmarshal(raw, &opt.Faults); err != nil {
			fail("faults: %v", err)
		}
	}

	srv, err := replay.New(sc, tr, opt)
	if err != nil {
		fail("%v", err)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fail("%v", err)
	}
	go func() { _ = http.Serve(ln, srv) }()
	fmt.Printf("replaying %s as %s on ws://%s/ (%d steps)\n", sc.Name, strings.Join(opt.Conns, ","), ln.Addr(), len(sc.Steps))

	rep := srv.Run()
	_ = ln.Close()
	for _, e := range rep.Events {
		fmt.Println("  event:", e)
	}
	for _, e := range rep.Errors {
		fmt.Println("  ERROR:", e)
	}
	if *report != "" {
		data, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*report, append(data, '\n'), 0o644); err != nil {
			fail("%v", err)
		}
	}
	if !rep.OK() {
		fmt.Printf("FAIL %s: %d/%d steps\n", sc.Name, rep.Completed, rep.Steps)
		os.Exit(1)
	}
	fmt.Printf("PASS %s: %d/%d steps\n", sc.Name, rep.Completed, rep.Steps)
}

func load(scDir, trDir, name string) (*harness.Scenario, *harness.Transcript) {
	all, err := harness.LoadScenarios(scDir)
	if err != nil {
		fail("%v", err)
	}
	for _, sc := range all {
		if sc.Name == name {
			tr, err := harness.ReadTranscript(filepath.Join(trDir, replay.TranscriptFile(name)))
			if err != nil {
				fail("%v", err)
			}
			return sc, tr
		}
	}
	fail("no scenario named %q", name)
	return nil, nil
}

func printBindings(b *replay.Bindings) {
	keys := b.Keys()
	sort.Strings(keys)
	for _, k := range keys {
		v, _ := b.Value(k)
		fmt.Printf("${%s} = %s\n", k, v)
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "replay: "+format+"\n", a...)
	os.Exit(2)
}
