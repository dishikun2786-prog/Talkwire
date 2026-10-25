package main

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// referenceSource is the reference server's source, frozen in the repository.
const referenceSource = "../tools/conformance/oracle/src/server/im-go"

// defaults reads the configurable defaults of a source tree: every
// envDuration/envInt default (by variable name), every field of
// defaultRuntimeConfig, and every field LoadConfig starts its Config with.
func defaults(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files in %s", dir)
	}
	fset := token.NewFileSet()
	out := map[string]string{}
	text := func(e ast.Expr) string {
		var b strings.Builder
		printer.Fprint(&b, fset, e)
		return b.String()
	}
	fields := func(prefix string, lit *ast.CompositeLit) {
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				out[prefix+text(kv.Key)] = text(kv.Value)
			}
		}
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && (id.Name == "envDuration" || id.Name == "envInt") && len(n.Args) == 2 {
					name, _ := strconv.Unquote(text(n.Args[0]))
					if inner, ok := n.Args[1].(*ast.CallExpr); ok {
						// A newer name falling back to an older one: the
						// default is the older one's.
						_ = inner
						out["env "+name] = "fallback"
					} else {
						out["env "+name] = text(n.Args[1])
					}
				}
			case *ast.FuncDecl:
				switch n.Name.Name {
				case "defaultRuntimeConfig", "LoadConfig":
					ast.Inspect(n.Body, func(m ast.Node) bool {
						if lit, ok := m.(*ast.CompositeLit); ok {
							if id, ok := lit.Type.(*ast.Ident); ok && (id.Name == "RuntimeConfig" || id.Name == "Config") {
								fields(id.Name+".", lit)
							}
						}
						return true
					})
				}
			}
			return true
		})
	}
	return out
}

// R9: every configurable value keeps the reference server's default.
func TestR9_DefaultsEqualOriginalValues(t *testing.T) {
	ref := defaults(t, referenceSource)
	cur := defaults(t, ".")
	// 7 environment knobs, 14 runtime-config fields, 7 startup-config fields.
	if len(ref) != 28 {
		t.Fatalf("read %d defaults from the reference source, want 28; the scan is broken", len(ref))
	}
	for k, v := range ref {
		if cur[k] != v {
			t.Errorf("%s: default %q, the reference server's is %q", k, cur[k], v)
		}
	}
	// Knobs the reference server does not have, each with why its default is
	// what it is.
	added := map[string]string{
		"env IMKIT_PUSH_HTTP_TIMEOUT_SEC": "fallback", // falls back to IM_FCM_HTTP_TIMEOUT_SEC (10s)
		"env IMKIT_WEBPUSH_TTL_SEC":       "24 * time.Hour",
		"Config.HostMode":                 `"http"`, // the reference has only the HTTP host
		"RuntimeConfig.PushText":          "defaultPushText()",
		// The reference allowed one connection per user (each login replaced
		// the last); 10 devices covers phones, tablets and browsers without
		// letting one account hold unbounded connections (task 1.12).
		"env IMKIT_MAX_DEVICES_PER_USER": "10",
	}
	for k, v := range cur {
		if _, inRef := ref[k]; inRef {
			continue
		}
		if want, ok := added[k]; !ok {
			t.Errorf("new configurable %s = %s: add it to the list with its reason", k, v)
		} else if v != want {
			t.Errorf("new configurable %s = %s, listed as %s", k, v, want)
		}
	}
	// The ones the list names must exist.
	for k := range added {
		if _, ok := cur[k]; !ok {
			t.Errorf("%s is listed as added but not found", k)
		}
	}
	// The push text defaults are the reference's literal Chinese wording;
	// TestDefaultPushTextIsTheReferenceWording compares them string by string.
}

// R11: the keepalive ping runs at 0.45 of the read deadline, and follows it
// when the deadline is configured (IM_PONG_WAIT_SEC).
func TestR11_PingIntervalIs045OfTimeout(t *testing.T) {
	if pingPeriod != pingPeriodFor(pongWait) {
		t.Errorf("pingPeriod %v is not derived from pongWait %v", pingPeriod, pongWait)
	}
	for _, deadline := range []time.Duration{60 * time.Second, 100 * time.Second, 20 * time.Second} {
		if got, want := pingPeriodFor(deadline), time.Duration(float64(deadline)*0.45); got != want {
			t.Errorf("deadline %v: ping every %v, want %v", deadline, got, want)
		}
		// Two pings always fit in one deadline window.
		if 2*pingPeriodFor(deadline) >= deadline {
			t.Errorf("deadline %v: two pings do not fit", deadline)
		}
	}
	if pongWait == 60*time.Second && pingPeriod != 27*time.Second {
		t.Errorf("default ping period %v, want 27s", pingPeriod)
	}
}
