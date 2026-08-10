package cogs

import (
	"bytes"
	gocontext "context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/joho/godotenv"
	"go.uber.org/multierr"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SecretManagerScheme prefixes a Link path that resolves against Google Secret Manager
const SecretManagerScheme = "gcpsm://"

// defaultSecretVersion is used when a GSM link declares no version alias
const defaultSecretVersion = "latest"

// SecretManagerConcurrency bounds the number of simultaneous Secret Manager fetches
var SecretManagerConcurrency int = 8

// SecretManagerTimeout bounds an entire batch of Secret Manager fetches, not an individual fetch
var SecretManagerTimeout time.Duration = 30 * time.Second

// isSecretManagerPath returns true if a Link path refers to Google Secret Manager
func isSecretManagerPath(path string) bool {
	return strings.HasPrefix(path, SecretManagerScheme)
}

// normalizeSecretManagerPath folds the version alias held by path[1] into the path:
// a GSM project+version is one document, so it groups, loads and reports errors
// like any other source
func normalizeSecretManagerPath(path, version string) (string, error) {
	project := strings.TrimPrefix(path, SecretManagerScheme)
	if before, after, ok := strings.Cut(project, "/"); ok {
		return "", fmt.Errorf("%s%s must only hold a project id, define the secret id with `name = %q`",
			SecretManagerScheme, before, after)
	}
	if project == "" {
		return "", fmt.Errorf("%s must be followed by a GCP project id", SecretManagerScheme)
	}
	if version == "" {
		version = defaultSecretVersion
	}
	return SecretManagerScheme + project + "/" + version, nil
}

// parseSecretManagerPath splits a path already run through normalizeSecretManagerPath
func parseSecretManagerPath(path string) (project, version string, err error) {
	project, version, ok := strings.Cut(strings.TrimPrefix(path, SecretManagerScheme), "/")
	if !ok || project == "" || version == "" {
		return "", "", fmt.Errorf("%q is not a %s<project>/<version> path", path, SecretManagerScheme)
	}
	return project, version, nil
}

// secretResource returns the Secret Manager resource name of a single secret version
func secretResource(project, secret, version string) string {
	return fmt.Sprintf("projects/%s/secrets/%s/versions/%s", project, secret, version)
}

// secretFetcher resolves one secret version resource name to its raw payload
type secretFetcher func(ctx gocontext.Context, resource string) ([]byte, error)

// newSecretFetcher builds a fetcher backed by one shared Secret Manager client.
// It is a package var so tests can stub Secret Manager without network or ADC.
var newSecretFetcher = func(ctx gocontext.Context) (secretFetcher, func(), error) {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("secret manager client: %w", err)
	}

	fetch := func(ctx gocontext.Context, resource string) ([]byte, error) {
		resp, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
			Name: resource,
		})
		if err != nil {
			return nil, err
		}
		if resp.GetPayload() == nil {
			return nil, fmt.Errorf("secret version returned an empty payload")
		}
		return resp.GetPayload().GetData(), nil
	}

	return fetch, func() { _ = client.Close() }, nil
}

// secretFetcherPool dials at most one Secret Manager client and hands it to every
// gcpsm:// group of a resolve pass: one client can serve any project, since the
// project lives in the resource name. The zero value is ready to use and dials
// lazily, so a manifest with no gcpsm:// link never reaches for ADC
type secretFetcherPool struct {
	mu       sync.Mutex
	fetch    secretFetcher
	closeCli func()
	err      error
	dialed   bool
}

// get returns the shared fetcher, dialing it on first call
func (p *secretFetcherPool) get() (secretFetcher, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dialed {
		p.dialed = true
		// Background, not a batch ctx: the client outlives any single batch and its
		// credentials refresh against the context it was dialed with
		p.fetch, p.closeCli, p.err = newSecretFetcher(gocontext.Background())
	}
	return p.fetch, p.err
}

// Close releases the client if one was ever dialed
func (p *secretFetcherPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closeCli != nil {
		p.closeCli()
		p.closeCli = nil
	}
}

// secretManagerError annotates a fetch failure with its coordinate.
// Payload bytes are never included in an error.
func secretManagerError(project, secret, version string, err error) error {
	coord := fmt.Sprintf("%s%s/%s#%s", SecretManagerScheme, project, secret, version)
	switch status.Code(err) {
	case codes.PermissionDenied:
		return fmt.Errorf("%s: permission denied reading secret %q in project %q "+
			"(try `gcloud auth application-default login`)", coord, secret, project)
	case codes.Unauthenticated:
		// expired or re-auth-required ADC surfaces here, not as PermissionDenied
		return fmt.Errorf("%s: could not authenticate to Secret Manager, "+
			"run `gcloud auth application-default login`: %w", coord, err)
	}
	return fmt.Errorf("%s: %w", coord, err)
}

// getSecretManagerFile is the loadFile func of a gcpsm:// path group. It returns the
// project+version document: a JSON object of secret id to base64 payload. Marshaling
// each payload as a JSON string is what keeps it verbatim - no value ever reaches a
// YAML parser, so "p@ss: word" stays a string and "*abc" is never an alias.
// The client comes from pool so sibling groups share one connection; pool owns closing it
func getSecretManagerFile(path string, links []*Link, pool *secretFetcherPool) ([]byte, error) {
	project, version, err := parseSecretManagerPath(path)
	if err != nil {
		return nil, err
	}

	// many links can share one secret id: the set collapses them into a single fetch
	idSet := make(map[string]struct{}, len(links))
	for _, link := range links {
		idSet[link.SearchName] = struct{}{}
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	// sorted ids keep fetch dispatch and error output identical run to run
	sort.Strings(ids)

	fetch, err := pool.get()
	if err != nil {
		return nil, err
	}

	// the timeout bounds this batch of fetches, never the shared client
	ctx, cancel := gocontext.WithTimeout(gocontext.Background(), SecretManagerTimeout)
	defer cancel()

	// index-addressed results: no shared map writes, nothing to guard with a mutex
	payloads := make([][]byte, len(ids))
	errs := make([]error, len(ids))

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(SecretManagerConcurrency)
	for i, id := range ids {
		eg.Go(func() error {
			payloads[i], errs[i] = fetch(egCtx, secretResource(project, id, version))
			// collect failures rather than cancelling siblings: a batch with several
			// bad secret ids should report all of them
			return nil
		})
	}
	_ = eg.Wait()

	doc := make(map[string]string, len(ids))
	var fetchErrs error
	for i, id := range ids {
		if err := errs[i]; err != nil {
			// a secret that does not exist is left out of the document so the visitor
			// reports it as a missing key, like a key absent from a YAML file
			if status.Code(err) == codes.NotFound {
				continue
			}
			fetchErrs = multierr.Append(fetchErrs, secretManagerError(project, id, version, err))
			continue
		}
		// a trailing newline must never leak into a value such as PGPASSWORD.
		// base64 keeps a non-UTF-8 payload intact: json.Marshal would otherwise
		// replace invalid bytes with U+FFFD
		doc[id] = base64.StdEncoding.EncodeToString(bytes.TrimRight(payloads[i], "\n"))
	}
	if fetchErrs != nil {
		return nil, fetchErrs
	}

	return json.Marshal(doc)
}

// secretVisitor resolves Links against the payloads fetched for one project+version.
// A payload is handed back verbatim unless the Link declares a read type, in which
// case the payload itself is parsed: a secret holding JSON becomes a map
type secretVisitor struct {
	payloads map[string]string
	missing  map[string][]string
}

// newSecretManagerVisitor returns a Visitor over a getSecretManagerFile document
func newSecretManagerVisitor(buf []byte) (Visitor, error) {
	encoded := make(map[string]string)
	if err := json.Unmarshal(buf, &encoded); err != nil {
		return nil, fmt.Errorf("newSecretManagerVisitor: %w", err)
	}
	payloads := make(map[string]string, len(encoded))
	for id, enc := range encoded {
		raw, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return nil, fmt.Errorf("newSecretManagerVisitor: %s: %w", id, err)
		}
		payloads[id] = string(raw)
	}
	return &secretVisitor{payloads: payloads, missing: make(map[string][]string)}, nil
}

func (vi *secretVisitor) Errors() []error {
	return missingErrors(vi.missing)
}

// SetValue assigns the payload of the secret named by link.SearchName
func (vi *secretVisitor) SetValue(link *Link) error {
	payload, ok := vi.payloads[link.SearchName]
	if !ok {
		noteMissing(vi.missing, link)
		return nil
	}

	// the entirety of a secret is its payload, so neither type touches it
	if link.readType == deferred || link.readType == rWhole {
		link.Value = payload
		return nil
	}

	value := make(map[string]interface{})
	if link.readType == rDotenv {
		flat, err := godotenv.Unmarshal(payload)
		if err != nil {
			return fmt.Errorf("%s: %w", link.SearchName, err)
		}
		for k, v := range flat {
			value[k] = v
		}
		link.Value = value
		return nil
	}

	unmarshal, err := link.readType.getUnmarshal()
	if err != nil {
		return fmt.Errorf("%s: %w", link.SearchName, err)
	}
	if err := unmarshal([]byte(payload), &value); err != nil {
		return fmt.Errorf("%s: %w", link.SearchName, err)
	}
	// a flat read type must not smuggle a nested object through
	if !link.readType.isComplex() {
		for k, v := range value {
			if !IsSimpleValue(v) {
				return fmt.Errorf("%s.%s of type %T is not a simple value", link.SearchName, k, v)
			}
		}
	}
	link.Value = value

	return nil
}
