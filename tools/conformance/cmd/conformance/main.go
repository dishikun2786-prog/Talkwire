// Command conformance records and checks protocol transcripts.
//
//	conformance record -bin oracle/bin/im-oracle.exe -out ../../contract/protocol/transcripts
//	conformance check  -bin <server> -golden ../../contract/protocol/transcripts
//	conformance repeat -bin oracle/bin/im-oracle.exe -n 3
//
// Every scenario runs against a freshly started server on an empty database,
// so ids the server allocates are reproducible.
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

	"example.com/imkit/tools/conformance/fakehost"
	"example.com/imkit/tools/conformance/harness"
)

// The secret only signs tokens for throwaway test databases.
const testSecret = "imkit-conformance-secret-not-for-production-0123456789"

type options struct {
	bin       string
	scenarios string
	db        harness.DB
	port      int
	hostPort  int
	only      string
	source    string
	logDir    string
	// standalone runs the server with no host process (IMKIT_HOST_MODE=standalone).
	standalone bool
	// env is extra environment for the server, e.g. IMKIT_MODULES=e2ee.
	env envList
	// additions names fields the server under test may add (see
	// harness.KnownAdditions); they are removed before comparing.
	additions string
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var o options
	fs.StringVar(&o.bin, "bin", "oracle/bin/im-oracle.exe", "server binary under test")
	fs.StringVar(&o.scenarios, "scenarios", "../../contract/protocol/scenarios", "scenario directory")
	fs.StringVar(&o.db.Host, "pghost", "127.0.0.1", "PostgreSQL host")
	fs.IntVar(&o.db.Port, "pgport", 55432, "PostgreSQL port")
	fs.StringVar(&o.db.User, "pguser", "postgres", "PostgreSQL user")
	fs.StringVar(&o.db.Name, "db", "imkit_oracle", "throwaway database (its public schema is dropped)")
	fs.IntVar(&o.port, "port", 16062, "port for the server under test")
	fs.IntVar(&o.hostPort, "hostport", 17001, "port for the fake host")
	fs.StringVar(&o.only, "only", "", "run only scenarios whose name contains this")
	fs.StringVar(&o.source, "source", "oracle 33e23250", "label stored in recorded transcripts")
	fs.StringVar(&o.logDir, "logs", "logs", "directory for server logs")
	fs.Var(&o.env, "env", "extra server environment KEY=VALUE (repeatable), e.g. -env IMKIT_MODULES=none")
	fs.StringVar(&o.additions, "additions", "", "check: fields the server may add, removed before comparing (strict = the strict protocol profile's)")
	fs.BoolVar(&o.standalone, "standalone", false, "check: run the server without a host (friends, profiles and runtime config from the scenario); host calls are not compared")
	out := fs.String("out", "../../contract/protocol/transcripts", "record: output directory")
	golden := fs.String("golden", "../../contract/protocol/transcripts", "check: golden directory")
	n := fs.Int("n", 3, "repeat: number of runs")
	schemaDir := fs.String("schema", "../../contract/protocol/schema", "coverage: protocol schemas")
	negative := fs.String("negative", "../schema/negative-cases.json", "coverage: steps that send invalid requests on purpose")
	oracleBin := fs.String("oracle", "oracle/bin/im-oracle.exe", "schema: the reference server")
	oracleDB := fs.String("oracle-db", "imkit_oracle", "schema: throwaway database for the reference server (must differ from -db)")
	_ = fs.Parse(os.Args[2:])

	scenarios, err := harness.LoadScenarios(o.scenarios)
	must(err)
	if o.only != "" {
		var kept []*harness.Scenario
		for _, sc := range scenarios {
			if strings.Contains(sc.Name, o.only) {
				kept = append(kept, sc)
			}
		}
		scenarios = kept
	}
	if cmd == "coverage" {
		os.Exit(coverage(scenarios, *golden, *schemaDir, *negative))
	}
	must(os.MkdirAll(o.logDir, 0o755))
	host := fakehost.New(testSecret)
	stopHost := serveHost(host, o.hostPort)
	defer stopHost()

	switch cmd {
	case "schema":
		os.Exit(schemaCheck(o, host, *oracleBin, *oracleDB))
	case "record":
		must(os.MkdirAll(*out, 0o755))
		for _, sc := range scenarios {
			tr, err := runOne(o, host, sc)
			must(err)
			path := filepath.Join(*out, fileName(sc.Name))
			must(tr.Write(path))
			fmt.Printf("RECORDED %-40s %d steps -> %s\n", sc.Name, len(tr.Steps), path)
		}
	case "check":
		failed := 0
		additions, stripping := harness.KnownAdditions[o.additions]
		if o.additions != "" && !stripping {
			fmt.Fprintf(os.Stderr, "unknown -additions %q\n", o.additions)
			os.Exit(2)
		}
		removed := map[string]int{}
		for _, sc := range scenarios {
			want, err := harness.ReadTranscript(filepath.Join(*golden, fileName(sc.Name)))
			if err != nil {
				fmt.Printf("MISSING  %-40s %v\n", sc.Name, err)
				failed++
				continue
			}
			got, err := runOne(o, host, sc)
			if err != nil {
				// A server that answers differently can make a later step
				// impossible (nothing to capture); that is a failure of this
				// scenario, not of the run.
				fmt.Printf("FAIL     %-40s %v\n", sc.Name, err)
				failed++
				continue
			}
			if o.standalone {
				dropHostCalls(want)
				dropHostCalls(got)
			}
			if stripping {
				for k, n := range got.Strip(additions) {
					removed[k] += n
				}
			}
			if diffs := harness.Compare(want, got); len(diffs) > 0 {
				failed++
				fmt.Printf("FAIL     %-40s %d difference(s)\n", sc.Name, len(diffs))
				for _, d := range diffs {
					fmt.Println("  " + d.String())
				}
				continue
			}
			fmt.Printf("PASS     %s\n", sc.Name)
		}
		if stripping {
			keys := make([]string, 0, len(removed))
			for k := range removed {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var parts []string
			for _, k := range keys {
				parts = append(parts, fmt.Sprintf("%s=%d", k, removed[k]))
			}
			fmt.Printf("ADDED    %s: %s\n", additions.Name, strings.Join(parts, " "))
		}
		fmt.Printf("SUMMARY  %d scenario(s), %d failed\n", len(scenarios), failed)
		if failed > 0 {
			os.Exit(1)
		}
	case "repeat":
		failed := 0
		for _, sc := range scenarios {
			var first *harness.Transcript
			ok := true
			for i := 0; i < *n; i++ {
				tr, err := runOne(o, host, sc)
				must(err)
				if first == nil {
					first = tr
					continue
				}
				if diffs := harness.Compare(first, tr); len(diffs) > 0 {
					ok = false
					fmt.Printf("UNSTABLE %-40s run %d differs from run 1\n", sc.Name, i+1)
					for _, d := range diffs {
						fmt.Println("  " + d.String())
					}
					break
				}
			}
			if ok {
				fmt.Printf("STABLE   %-40s %d runs identical\n", sc.Name, *n)
			} else {
				failed++
			}
		}
		fmt.Printf("SUMMARY  %d scenario(s), %d unstable\n", len(scenarios), failed)
		if failed > 0 {
			os.Exit(1)
		}
	default:
		usage()
	}
}

func runOne(o options, host *fakehost.Host, sc *harness.Scenario) (*harness.Transcript, error) {
	if err := o.db.Reset(); err != nil {
		return nil, fmt.Errorf("reset db: %w", err)
	}
	host.Reset()
	for _, f := range sc.Host.Friends {
		host.SetFriends(harness.AppUserID(f[0]), harness.AppUserID(f[1]), true)
	}
	for u, p := range sc.Host.Profiles {
		host.SetProfile(harness.AppUserID(u), p)
	}
	if len(sc.Host.RuntimeConfig) > 0 {
		host.SetRuntimeConfig(json.RawMessage(sc.Host.RuntimeConfig))
	}
	cfg := harness.ServerConfig{
		Bin:     o.bin,
		Port:    o.port,
		DB:      o.db,
		Secret:  testSecret,
		HostURL: fmt.Sprintf("http://127.0.0.1:%d", o.hostPort),
		LogPath: filepath.Join(o.logDir, fileName(sc.Name)+".log"),
	}
	cfg.Env = append(cfg.Env, o.env...)
	if o.standalone {
		path, err := writeStandaloneConfig(o.logDir, sc)
		if err != nil {
			return nil, err
		}
		// Nothing listens on port 1: a host call the server should not make
		// fails loudly instead of being answered by the fake host.
		cfg.HostURL = "http://127.0.0.1:1"
		cfg.Env = append(cfg.Env, "IMKIT_HOST_MODE=standalone", "IMKIT_STANDALONE_CONFIG="+path)
	}
	srv, err := harness.StartServer(cfg)
	if err != nil {
		return nil, err
	}
	defer srv.Stop()
	tr, err := harness.Run(sc, harness.Env{
		WSURL:    srv.WSURL(),
		HTTPBase: srv.HTTPBase(),
		Secret:   testSecret,
		Host:     host,
		Source:   o.source,
	})
	if err != nil {
		return nil, err
	}
	tr.Normalize(sc)
	return tr, nil
}

func serveHost(h *fakehost.Host, port int) func() {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	must(err)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return func() { _ = srv.Close() }
}

func fileName(name string) string {
	return strings.ReplaceAll(name, "/", "__") + ".json"
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: conformance record|check|repeat|coverage|schema [flags]")
	os.Exit(2)
}

// coverage reports, for every frame name in the protocol schemas, how many
// golden steps or frames use it. It needs no server. Exit status 1 when a name
// has no golden case or the goldens use a name the schemas do not define.
func coverage(scenarios []*harness.Scenario, golden, schemaDir, negative string) int {
	v, err := harness.LoadVocabulary(schemaDir)
	must(err)
	skip, err := harness.LoadStepSet(negative)
	must(err)
	trs := map[string]*harness.Transcript{}
	for _, sc := range scenarios {
		tr, err := harness.ReadTranscript(filepath.Join(golden, fileName(sc.Name)))
		must(err)
		trs[sc.Name] = tr
	}
	missing := 0
	for _, u := range harness.Coverage(v, scenarios, trs, skip) {
		where := strings.Join(u.Where, ", ")
		if len(u.Where) > 3 {
			where = strings.Join(u.Where[:3], ", ") + fmt.Sprintf(" and %d more", len(u.Where)-3)
		}
		status := "OK  "
		if u.Count == 0 {
			status = "NONE"
			missing++
		}
		fmt.Printf("%s %-6s %-32s %4d  %s\n", status, u.Side, u.Name, u.Count, where)
	}
	unlisted := harness.Unlisted(v, scenarios, trs, skip)
	for _, name := range unlisted {
		fmt.Printf("UNDEFINED %s (used by the goldens, not in the schemas)\n", name)
	}
	fmt.Printf("SUMMARY  %d client actions, %d server frame names, %d without a golden case, %d undefined\n",
		len(v.Client), len(v.Server), missing, len(unlisted))
	if missing > 0 || len(unlisted) > 0 {
		return 1
	}
	return 0
}

// writeStandaloneConfig turns the scenario's host setup into the standalone
// host's JSON file. Relationship changes made by later steps are not
// reflected: a standalone whitelist is fixed at startup.
func writeStandaloneConfig(dir string, sc *harness.Scenario) (string, error) {
	type profile struct {
		DisplayName string `json:"displayName"`
		AvatarURL   string `json:"avatarUrl"`
	}
	doc := struct {
		Relations     string             `json:"relations"`
		Friends       [][2]string        `json:"friends"`
		Profiles      map[string]profile `json:"profiles,omitempty"`
		RuntimeConfig json.RawMessage    `json:"runtimeConfig,omitempty"`
	}{Relations: "whitelist", Friends: [][2]string{}}
	for _, f := range sc.Host.Friends {
		doc.Friends = append(doc.Friends, [2]string{harness.AppUserID(f[0]), harness.AppUserID(f[1])})
	}
	for u, p := range sc.Host.Profiles {
		if doc.Profiles == nil {
			doc.Profiles = map[string]profile{}
		}
		doc.Profiles[harness.AppUserID(u)] = profile{DisplayName: p.DisplayName, AvatarURL: p.AvatarURL}
	}
	if len(sc.Host.RuntimeConfig) > 0 {
		doc.RuntimeConfig = json.RawMessage(sc.Host.RuntimeConfig)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(dir, fileName(sc.Name)+".standalone.json"))
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o644)
}

// dropHostCalls removes the recorded host calls; a standalone server makes none.
func dropHostCalls(tr *harness.Transcript) {
	for i := range tr.Steps {
		tr.Steps[i].HostCalls = nil
	}
}

// The server's additions to the reference schema: whole tables, and columns
// ("table.column") added to reference tables.
var (
	schemaAddedTables  = []string{"imkit_schema_migrations"}
	schemaAddedColumns = []string{"im_fcm_tokens.provider"}
)

// withoutAdditions is a catalog as the reference server would have it.
func withoutAdditions(c harness.Catalog) harness.Catalog {
	return c.Without(schemaAddedTables...).WithoutColumns(schemaAddedColumns...)
}

// schemaCheck compares the schema the server builds with the reference
// server's: on an empty database, after a restart (which must apply nothing),
// and on a database the reference server already created (the upgrade path
// of an existing deployment), where the result must also equal the empty
// database's, additions included.
func schemaCheck(o options, host *fakehost.Host, oracleBin, oracleDB string) int {
	ref := o.db
	ref.Name = oracleDB
	if ref.Name == o.db.Name {
		fmt.Fprintln(os.Stderr, "schema: -db and -oracle-db must be different databases")
		return 2
	}
	start := func(bin string, db harness.DB, tag string) string {
		host.Reset()
		logPath := filepath.Join(o.logDir, "schema-"+tag+".log")
		srv, err := harness.StartServer(harness.ServerConfig{
			Bin: bin, Port: o.port, DB: db, Secret: testSecret,
			HostURL: fmt.Sprintf("http://127.0.0.1:%d", o.hostPort), LogPath: logPath,
		})
		must(err)
		srv.Stop()
		return logPath
	}
	catalog := func(db harness.DB) harness.Catalog {
		c, err := db.Catalog()
		must(err)
		return c
	}
	failed := 0
	compare := func(label string, want, got harness.Catalog) {
		if d := want.Diff(got); len(d) > 0 {
			failed++
			fmt.Printf("FAIL     %s: %d difference(s)\n", label, len(d))
			for _, l := range d {
				fmt.Println("  " + l)
			}
			return
		}
		fmt.Printf("PASS     %s (%d lines)\n", label, len(want.Lines))
	}

	must(ref.Reset())
	start(oracleBin, ref, "reference")
	reference := catalog(ref)

	must(o.db.Reset())
	start(o.bin, o.db, "fresh")
	fresh := catalog(o.db)
	compare("empty database: server vs reference", reference, withoutAdditions(fresh))
	added := len(fresh.Lines) - len(withoutAdditions(fresh).Lines)
	additions := append(append([]string{}, schemaAddedTables...), schemaAddedColumns...)
	fmt.Printf("         additions: %s (%d lines)\n", strings.Join(additions, ", "), added)

	restartLog := start(o.bin, o.db, "restart")
	compare("restart: unchanged", fresh, catalog(o.db))
	data, err := os.ReadFile(restartLog)
	must(err)
	if n := strings.Count(string(data), "applied migration"); n > 0 {
		failed++
		fmt.Printf("FAIL     restart: applied %d migration(s) again\n", n)
	} else {
		fmt.Println("PASS     restart: no migration applied again")
	}

	must(o.db.Reset())
	start(oracleBin, o.db, "adopt-reference")
	start(o.bin, o.db, "adopt")
	adopted := catalog(o.db)
	compare("existing reference database: adopted vs reference", reference, withoutAdditions(adopted))
	compare("existing reference database: adopted vs empty database", fresh, adopted)

	fmt.Printf("SUMMARY  %d check(s) failed\n", failed)
	if failed > 0 {
		return 1
	}
	return 0
}

// envList collects repeated -env flags.
type envList []string

func (e *envList) String() string { return strings.Join(*e, " ") }

func (e *envList) Set(v string) error {
	if !strings.Contains(v, "=") {
		return fmt.Errorf("want KEY=VALUE, got %q", v)
	}
	*e = append(*e, v)
	return nil
}
