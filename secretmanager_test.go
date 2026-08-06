package cogs

import (
	gocontext "context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/pelletier/go-toml"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stubFetcher records every coordinate requested and serves payloads from a fixed map
type stubFetcher struct {
	payloads map[string]string // secret id -> payload
	failures map[string]error  // secret id -> error

	mu     sync.Mutex
	calls  []smCoord
	closed bool
}

func (s *stubFetcher) fetch(_ gocontext.Context, c smCoord) ([]byte, error) {
	s.mu.Lock()
	s.calls = append(s.calls, c)
	s.mu.Unlock()

	if err, ok := s.failures[c.secret]; ok {
		return nil, secretManagerError(c, err)
	}
	payload, ok := s.payloads[c.secret]
	if !ok {
		return nil, secretManagerError(c, fmt.Errorf("stub has no payload"))
	}
	return []byte(payload), nil
}

func (s *stubFetcher) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// install replaces newSecretFetcher for the duration of a test
func (s *stubFetcher) install(t *testing.T) {
	t.Helper()
	orig := newSecretFetcher
	newSecretFetcher = func(gocontext.Context) (secretFetcher, func(), error) {
		return s.fetch, func() { s.closed = true }, nil
	}
	t.Cleanup(func() { newSecretFetcher = orig })
}

// genGear resolves a cog manifest held in a string, returning the gear so private
// Link fields stay assertable
func genGear(t *testing.T, ctxName, cogToml string) (*Gear, CfgMap, error) {
	t.Helper()
	tree, err := toml.Load(cogToml)
	if err != nil {
		t.Fatalf("toml.Load: %v", err)
	}
	gear := &Gear{
		filePath:   "test.cog.toml",
		fileValue:  []byte(cogToml),
		tree:       tree,
		outputType: Raw,
		filter:     func(linkMap LinkMap) (LinkMap, error) { return linkMap, nil },
	}
	cfg, err := generate(ctxName, tree, gear)
	return gear, cfg, err
}

const sharedCtxCogToml = `
name = "sm"

[qa.enc]
path = ["gcpsm://bestow-secrets-nonprod", "current"]

[qa.enc.vars]
pas = {path = [], name = "ryerson-tools-PAS_RO_DB__PASSWORD-qa"}
crs = {path = [], name = "ryerson-tools-CRS_RO_DB__PASSWORD-qa"}
dcs = {path = [], name = "ryerson-tools-DCS_RO_DB__PASSWORD-qa"}
`

// Vars sharing a ctx-level path differ only by name, which distinctPath does not
// account for: a document-oriented resolver collapses them into one fetch and hands
// back the same value three times.
func TestSecretManagerSharedCtxResolvesDistinctSecrets(t *testing.T) {
	stub := &stubFetcher{payloads: map[string]string{
		"ryerson-tools-PAS_RO_DB__PASSWORD-qa": "pas-pw",
		"ryerson-tools-CRS_RO_DB__PASSWORD-qa": "crs-pw",
		"ryerson-tools-DCS_RO_DB__PASSWORD-qa": "dcs-pw",
	}}
	stub.install(t)

	gear, cfg, err := genGear(t, "qa", sharedCtxCogToml)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	want := map[string]string{"pas": "pas-pw", "crs": "crs-pw", "dcs": "dcs-pw"}
	for key, wantVal := range want {
		if cfg[key] != wantVal {
			t.Errorf("%s = %q, want %q", key, cfg[key], wantVal)
		}
	}
	if got := stub.callCount(); got != 3 {
		t.Errorf("fetch calls = %d, want 3", got)
	}
	if !stub.closed {
		t.Error("secret manager client was not closed")
	}

	for key, link := range gear.linkMap {
		if !link.secretManager {
			t.Errorf("%s: secretManager = false, want true", key)
		}
		if link.remote {
			t.Errorf("%s: remote = true, a gcpsm:// link must not be fetched over HTTP", key)
		}
		if link.smProject != "bestow-secrets-nonprod" {
			t.Errorf("%s: smProject = %q", key, link.smProject)
		}
		if got := link.smCoord().version; got != "current" {
			t.Errorf("%s: version = %q, want current", key, got)
		}
	}
}

// Payloads that a YAML visitor would reinterpret as maps, ints, anchors, or tags
func TestSecretManagerPayloadsSurviveVerbatim(t *testing.T) {
	testCases := []struct {
		name    string
		payload string
		want    string
	}{
		{name: "Plain", payload: "hunter2", want: "hunter2"},
		{name: "ColonSpace", payload: "p@ss: word", want: "p@ss: word"},
		{name: "Integer", payload: "123", want: "123"},
		{name: "Anchor", payload: "*abc", want: "*abc"},
		{name: "Tag", payload: "!!weird", want: "!!weird"},
		{name: "TrailingNewline", payload: "hunter2\n", want: "hunter2"},
		{name: "InternalNewline", payload: "line1\nline2\n", want: "line1\nline2"},
		{name: "Empty", payload: "", want: ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubFetcher{payloads: map[string]string{"secret": tc.payload}}
			stub.install(t)

			_, cfg, err := genGear(t, "qa", `
name = "sm"
[qa.enc.vars]
pw = {path = ["gcpsm://proj", "latest"], name = "secret"}
`)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			if cfg["pw"] != tc.want {
				t.Errorf("pw = %q, want %q", cfg["pw"], tc.want)
			}
		})
	}
}

func TestSecretManagerDedupsSharedCoordinate(t *testing.T) {
	stub := &stubFetcher{payloads: map[string]string{"shared": "shared-pw"}}
	stub.install(t)

	_, cfg, err := genGear(t, "qa", `
name = "sm"
[qa.enc]
path = ["gcpsm://proj", "current"]
[qa.enc.vars]
dcs_owner = {path = [], name = "shared"}
ns_owner  = {path = [], name = "shared"}
`)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if cfg["dcs_owner"] != "shared-pw" || cfg["ns_owner"] != "shared-pw" {
		t.Errorf("got %v", cfg)
	}
	if got := stub.callCount(); got != 1 {
		t.Errorf("fetch calls = %d, want 1 (coordinates must dedup)", got)
	}
}

func TestSecretManagerReportsEveryFailure(t *testing.T) {
	stub := &stubFetcher{
		payloads: map[string]string{"good": "ok"},
		failures: map[string]error{
			"bad-a": fmt.Errorf("boom a"),
			"bad-b": fmt.Errorf("boom b"),
		},
	}
	stub.install(t)

	cogToml := `
name = "sm"
[qa.enc]
path = ["gcpsm://proj", "current"]
[qa.enc.vars]
ok  = {path = [], name = "good"}
one = {path = [], name = "bad-a"}
two = {path = [], name = "bad-b"}
`
	_, _, err := genGear(t, "qa", cogToml)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"boom a", "boom b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	// sorted coordinate order keeps the message identical run to run
	if strings.Index(err.Error(), "boom a") > strings.Index(err.Error(), "boom b") {
		t.Errorf("errors are not in sorted coordinate order: %v", err)
	}
}

func TestSecretManagerNotFoundNamesSecretAndProject(t *testing.T) {
	stub := &stubFetcher{failures: map[string]error{
		"missing": status.Error(codes.NotFound, "not found"),
	}}
	stub.install(t)

	_, _, err := genGear(t, "qa", `
name = "sm"
[qa.enc.vars]
pw = {path = ["gcpsm://proj", "current"], name = "missing"}
`)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"missing", "proj", "not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestSecretManagerManifestErrors(t *testing.T) {
	testCases := []struct {
		name    string
		cogToml string
		wantErr string
	}{
		{
			name: "ExplicitType",
			cogToml: `
name = "sm"
[qa.enc.vars]
pw = {path = ["gcpsm://proj", "current"], name = "secret", type = "whole"}
`,
			wantErr: "type is not supported",
		},
		{
			name: "SecretInPath",
			cogToml: `
name = "sm"
[qa.enc.vars]
pw = {path = ["gcpsm://proj/secret", "current"]}
`,
			wantErr: "must only hold a project id",
		},
		{
			name: "NoProject",
			cogToml: `
name = "sm"
[qa.enc.vars]
pw = {path = ["gcpsm://", "current"]}
`,
			wantErr: "must be followed by a GCP project id",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubFetcher{payloads: map[string]string{"secret": "pw"}}
			stub.install(t)

			_, _, err := genGear(t, "qa", tc.cogToml)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
			if stub.callCount() != 0 {
				t.Error("a manifest error must be caught before any fetch")
			}
		})
	}
}

// A GSM link outside of an .enc block resolves identically to one inside it
func TestSecretManagerOutsideEncBlock(t *testing.T) {
	stub := &stubFetcher{payloads: map[string]string{"secret": "pw"}}
	stub.install(t)

	_, cfg, err := genGear(t, "qa", `
name = "sm"
[qa.vars]
pw = {path = ["gcpsm://proj", "current"], name = "secret"}
`)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if cfg["pw"] != "pw" {
		t.Errorf("pw = %q", cfg["pw"])
	}
}

// NoDecrypt has no ciphertext form to hand back for a GSM secret, so the fetch still happens
func TestSecretManagerIgnoresNoDecrypt(t *testing.T) {
	stub := &stubFetcher{payloads: map[string]string{"secret": "pw"}}
	stub.install(t)

	NoDecrypt = true
	t.Cleanup(func() { NoDecrypt = false })

	_, cfg, err := genGear(t, "qa", `
name = "sm"
[qa.enc.vars]
pw = {path = ["gcpsm://proj", "current"], name = "secret"}
`)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if cfg["pw"] != "pw" {
		t.Errorf("pw = %q, want the fetched value", cfg["pw"])
	}
}

func TestSecretManagerConcurrentFetches(t *testing.T) {
	payloads := make(map[string]string)
	vars := &strings.Builder{}
	want := make(map[string]string)
	for i := 0; i < 40; i++ {
		key := fmt.Sprintf("var%02d", i)
		secret := fmt.Sprintf("secret-%02d", i)
		payloads[secret] = fmt.Sprintf("payload-%02d", i)
		want[key] = payloads[secret]
		fmt.Fprintf(vars, "%s = {path = [], name = %q}\n", key, secret)
	}
	stub := &stubFetcher{payloads: payloads}
	stub.install(t)

	cogToml := fmt.Sprintf(`
name = "sm"
[qa.enc]
path = ["gcpsm://proj", "current"]
[qa.enc.vars]
%s`, vars.String())

	_, cfg, err := genGear(t, "qa", cogToml)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for key, wantVal := range want {
		if cfg[key] != wantVal {
			t.Errorf("%s = %q, want %q", key, cfg[key], wantVal)
		}
	}
	if got := stub.callCount(); got != 40 {
		t.Errorf("fetch calls = %d, want 40", got)
	}
}

func TestSecretManagerConcurrencyOneMatchesFanOut(t *testing.T) {
	payloads := map[string]string{"a": "1", "b": "2", "c": "3"}
	cogToml := `
name = "sm"
[qa.enc]
path = ["gcpsm://proj", "current"]
[qa.enc.vars]
a = {path = [], name = "a"}
b = {path = [], name = "b"}
c = {path = [], name = "c"}
`
	results := make([]CfgMap, 0, 2)
	for _, limit := range []int{1, 8} {
		stub := &stubFetcher{payloads: payloads}
		stub.install(t)

		SecretManagerConcurrency = limit
		t.Cleanup(func() { SecretManagerConcurrency = 8 })

		_, cfg, err := genGear(t, "qa", cogToml)
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		results = append(results, cfg)
	}
	if fmt.Sprint(results[0]) != fmt.Sprint(results[1]) {
		t.Errorf("output differs by concurrency limit: %v vs %v", results[0], results[1])
	}
}
