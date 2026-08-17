package cogs

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// composeKeyRe matches keys usable as environment variable names: Docker splits
// each line on the first "=", so anything outside this charset yields a
// variable the container cannot reference
var composeKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// MarshalCompose renders a flat map as a Docker env-file: bare KEY=VALUE lines,
// no quoting and no escaping, sorted by key. Docker's env-file parser takes
// everything after the first "=" literally, so quotes, spaces, "#" and "$" all
// pass through unchanged.
//
// Values that cannot be represented - those holding a newline, carriage return,
// or NUL byte - return an error naming the key rather than a broken file.
//
// Gotcha: when the file is passed as `docker compose --env-file` (interpolation
// into compose.yaml) rather than `env_file`, Compose expands $VAR and ${VAR} in
// values. Escaping "$" would break the far more common env_file case, so cogs
// does not.
func MarshalCompose(envMap map[string]string) (string, error) {
	keys := make([]string, 0, len(envMap))
	for k := range envMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		if !composeKeyRe.MatchString(k) {
			return "", fmt.Errorf("MarshalCompose: invalid key name: %q", k)
		}
		v := envMap[k]
		if i := strings.IndexAny(v, "\n\r\x00"); i != -1 {
			return "", fmt.Errorf(
				"MarshalCompose: key %s: value contains %q, which a docker env-file cannot represent",
				k, v[i:i+1])
		}
		lines = append(lines, k+"="+v)
	}
	return strings.Join(lines, "\n"), nil
}
