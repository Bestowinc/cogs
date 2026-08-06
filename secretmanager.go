package cogs

import (
	"bytes"
	gocontext "context"
	"fmt"
	"sort"
	"strings"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
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

// parseSecretManagerPath returns the GCP project held by a gcpsm:// path
func parseSecretManagerPath(path string) (string, error) {
	project := strings.TrimPrefix(path, SecretManagerScheme)
	if before, after, ok := strings.Cut(project, "/"); ok {
		return "", fmt.Errorf("%s%s must only hold a project id, define the secret id with `name = %q`",
			SecretManagerScheme, before, strings.TrimPrefix(after, "/"))
	}
	if project == "" {
		return "", fmt.Errorf("%s must be followed by a GCP project id", SecretManagerScheme)
	}
	return project, nil
}

// smCoord fully identifies one secret version. It is comparable, so it doubles as a dedup map key
type smCoord struct {
	project string
	secret  string
	version string
}

// smCoord returns the Secret Manager coordinate a Link resolves to
func (c Link) smCoord() smCoord {
	version := c.SubPath
	if version == "" {
		version = defaultSecretVersion
	}
	return smCoord{project: c.smProject, secret: c.SearchName, version: version}
}

// resource returns the Secret Manager resource name of a coordinate
func (c smCoord) resource() string {
	return fmt.Sprintf("projects/%s/secrets/%s/versions/%s", c.project, c.secret, c.version)
}

// String holds the manifest-facing representation of a coordinate, used in error messages
func (c smCoord) String() string {
	return fmt.Sprintf("%s%s/%s#%s", SecretManagerScheme, c.project, c.secret, c.version)
}

// less orders coordinates so that fetch dispatch, value assignment, and error
// aggregation are all byte-identical run to run
func (c smCoord) less(o smCoord) bool {
	if c.project != o.project {
		return c.project < o.project
	}
	if c.secret != o.secret {
		return c.secret < o.secret
	}
	return c.version < o.version
}

// secretFetcher resolves a single coordinate to its raw payload
type secretFetcher func(ctx gocontext.Context, c smCoord) ([]byte, error)

// newSecretFetcher builds a fetcher backed by one shared Secret Manager client.
// It is a package var so tests can stub Secret Manager without network or ADC.
var newSecretFetcher = func(ctx gocontext.Context) (secretFetcher, func(), error) {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("secret manager client: %w", err)
	}

	fetch := func(ctx gocontext.Context, c smCoord) ([]byte, error) {
		resp, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
			Name: c.resource(),
		})
		if err != nil {
			return nil, secretManagerError(c, err)
		}
		if resp.GetPayload() == nil {
			return nil, fmt.Errorf("%s: secret version returned an empty payload", c)
		}
		return resp.GetPayload().GetData(), nil
	}

	return fetch, func() { _ = client.Close() }, nil
}

// secretManagerError annotates a fetch failure with its coordinate.
// Payload bytes are never included in an error.
func secretManagerError(c smCoord, err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return fmt.Errorf("%s: secret %q version %q not found in project %q",
			c, c.secret, c.version, c.project)
	case codes.PermissionDenied:
		return fmt.Errorf("%s: permission denied reading secret %q in project %q "+
			"(try `gcloud auth application-default login`)", c, c.secret, c.project)
	case codes.Unauthenticated:
		// expired or re-auth-required ADC surfaces here, not as PermissionDenied
		return fmt.Errorf("%s: could not authenticate to Secret Manager, "+
			"run `gcloud auth application-default login`: %w", c, err)
	}
	return fmt.Errorf("%s: %w", c, err)
}

// resolveSecretManager fetches every GSM link concurrently and assigns each payload
// verbatim to Link.Value, bypassing the document visitor entirely
func resolveSecretManager(links []*Link) error {
	// many links can share one coordinate: fetch each distinct secret once
	byCoord := make(map[smCoord][]*Link)
	for _, link := range links {
		if link.SearchName == "" {
			return fmt.Errorf("%s: secret id must be defined with the `name` key", link.KeyName)
		}
		coord := link.smCoord()
		byCoord[coord] = append(byCoord[coord], link)
	}

	coords := make([]smCoord, 0, len(byCoord))
	for coord := range byCoord {
		coords = append(coords, coord)
	}
	sort.Slice(coords, func(i, j int) bool { return coords[i].less(coords[j]) })

	ctx, cancel := gocontext.WithTimeout(gocontext.Background(), SecretManagerTimeout)
	defer cancel()

	fetch, closeFetcher, err := newSecretFetcher(ctx)
	if err != nil {
		return err
	}
	defer closeFetcher()

	// index-addressed results: no shared map writes, nothing to guard with a mutex
	payloads := make([][]byte, len(coords))
	errs := make([]error, len(coords))

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(SecretManagerConcurrency)
	for i, coord := range coords {
		eg.Go(func() error {
			payloads[i], errs[i] = fetch(egCtx, coord)
			// collect failures rather than cancelling siblings: a batch with several
			// bad secret ids should report all of them
			return nil
		})
	}
	_ = eg.Wait()

	var fetchErrs error
	for i, coord := range coords {
		if errs[i] != nil {
			fetchErrs = multierr.Append(fetchErrs, errs[i])
			continue
		}
		// a trailing newline must never leak into a value such as PGPASSWORD
		value := string(bytes.TrimRight(payloads[i], "\n"))
		for _, link := range byCoord[coord] {
			link.Value = value
		}
	}

	return fetchErrs
}
