COGS: COnfiguration manaGement S
---
`cogs` is a cli tool that allows generation of configuration files through different references sources.

Sources of reference can include:

* local files
* remote files (through [HTTP requests](examples/2.http.cog.toml))
* [SOPS encrypted files][sops] (can also be remote)
* [Google Secret Manager](#google-secret-manager) secrets

`cogs` allows one to deduplicate sources of truth by maintaining a **source of reference** (the cog file) that points to the location of values (such as port numbers and password strings).

## installation:

### With `go`:

Clone this repo and `cd` into it.

```sh
go build -o $GOPATH/bin/ ./cmd/cogs
```

### Without `go`

`PL`atform can be Linux/Windows/Darwin:

```sh
PL="Darwin" VR="0.9.1" \
  curl -SLk \
  "github.com/Bestowinc/cogs/releases/download/v${VR}/cogs_${VR}_${PL}_x86_64.tar.gz" | \
  tar xvz -C /usr/local/bin cogs
```

## help string:

```
COGS COnfiguration manaGement S

Usage:
  cogs gen <ctx> <cog-file> [options]

Options:
  -h --help        Show this screen.
  --version        Show version.
  --no-enc, -n     Skips fetching encrypted vars.
  --no-decrypt	   Skips decrypting encrypted vars.
  --envsubst, -e   Perform environmental substitution on the given cog file.
  --keys=<key,>    Include specific keys, comma separated.
  --not=<key,>     Exclude specific keys, comma separated.
  --out=<type>     Configuration output type [default: json].
                   <type>: json, toml, yaml, dotenv, compose, raw.

  --export, -x     If --out=dotenv:  Prepends "export " to each line.
  --preserve, -p   If --out=dotenv|compose: Preserves variable casing.
  --sep=<sep>      If --out=raw:     Delimits values with a <sep>arator.
```

## env output formats:

`--out=dotenv` targets a shell: values are quoted and escaped so the file can be
sourced. `--out=compose` targets Docker's env-file parser, which does not strip
quotes - `KEY="value"` would reach the container with the quotes intact - so it
writes bare `KEY=VALUE` lines with no quoting or escaping:

```sh
# shell-sourceable, values quoted and escaped
cogs gen prod ./cog.toml --out=dotenv > .env.sh

# Docker env-file: bare KEY=VALUE, consumed as-is by Compose env_file
cogs gen prod ./cog.toml --out=compose > .env
```

Docker env-files are line oriented with no escape sequences, so a value holding
a newline, carriage return, or NUL byte errors out naming the key rather than
producing a silently broken file.

`cogs gen` - outputs a flat and serialized K:V array

## [annotated spec](./examples/1.basic.cog.toml):

```toml
 # every cog manifest should have a name key that corresponds to a string
name = "basic example"

# key value pairs for a context/ctx are defined under <ctx>.vars
# try running `cogs gen basic ./examples/1.basic.cog.toml` to see what output
# cogs generates
[basic.vars]
var = "var_value"
other_var = "other_var_value"

# if <var>.path is given a string value,
# cogs will look for the key name of <var> in the file that that corresponds to
# the <var>.path key,
# returning the corresponding value
manifest_var.path = "../test_files/manifest.yaml"
# try removing manifest_var from "./test_files/manifest.yaml" and see what happens

# some variables can set an explicit key name to look for instead of defaulting
# to look for the key name "<var>":
# if <var>.name is defined then cogs will look for a key name that matches <var>.name
look_for_manifest_var.path = "../test_files/manifest.yaml"
look_for_manifest_var.name = "manifest_var"

# dangling variable names should return an error
# uncomment the line below and run `cogs gen basic ./examples/1.basic.cog.toml`:
# empty_var.name = "some_name"
```

## example data:

The example data (in `./examples`) are ordered by increasing complexity and should be used as a tutorial. Run `cogs gen` on the files in the order below,
then read the file to see how the underlying logic is used.

1. basic example:
   * `cogs gen basic 1.basic.cog.toml`
1. HTTP examples:
   * `cogs gen get 2.http.cog.toml`, GET example 
   * `cogs gen post 2.http.cog.toml`, POST example:
1. secret values and paths example:
   * `gpg --import ./test_files/sops_functional_tests_key.asc` should be run to import the test private key used for encrypted dummy data
   * `cogs gen sops 3.secrets.cog.toml`
1. read types example:
   * `cogs gen kustomize 4.read_types.cog.toml`
1. advanced patterns example:
   * `cogs gen complex_json 5.advanced.cog.toml`
1. envsubst patterns example:
   * `NVIM=nvim cogs gen envsubst 6.envsubst.cog.toml --envsubst`
1. Google Secret Manager example (needs your own GCP project and secrets):
   * `PROJECT=my-project cogs gen gsm 7.secret_manager.cog.toml -e`

## Google Secret Manager

A variable whose path starts with `gcpsm://` is fetched from Google Secret Manager instead of being read out of a document. No new manifest keys are involved — the three link fields carry the coordinate:

| cogs field | GSM meaning |
| --- | --- |
| `path[0]` | store + project — `gcpsm://<project>` |
| `path[1]` | version or alias — `latest`, `current`, `7` (defaults to `latest`) |
| `name`    | the secret id |

Project and version alias are normally shared at the ctx level so only the secret id varies per var; `path = []` inherits both.

```toml
[qa.enc]
path = ["gcpsm://bestow-secrets-nonprod", "current"]

[qa.enc.vars]
pas = {path = [], name = "ryerson-tools-PAS_RO_DB__PASSWORD-qa"}
crs = {path = [], name = "ryerson-tools-CRS_RO_DB__PASSWORD-qa"}

# a one-off var gives all three fields inline
[other.enc.vars]
db_password = {path = ["gcpsm://my-proj", "latest"], name = "db-password"}
# a secret whose payload is a JSON object
creds = {path = ["gcpsm://my-proj", "latest"], name = "db-creds", type = "json"}
```

Authentication uses [Application Default Credentials][adc] — the same credential path KMS-backed SOPS decryption already uses, so `gcloud auth application-default login` is the only setup. The `gcloud` binary is not invoked.

Behavior worth knowing:

* **Payloads are assigned verbatim.** A secret is never handed to a YAML parser, so `p@ss: word` stays a string rather than becoming a map, `123` stays `"123"`, and `*abc` does not resolve as an anchor.
* **`type` parses the payload itself.** A project + version alias is one document and each secret id is a key in it, so a var with no `type` gets its payload as a string. Declaring `type = "json"` (or `yaml`, `toml`, `dotenv`, `json{}`, …) parses that one secret's payload, which is how a secret holding `{"user":"u","pass":"p"}` becomes a map. `type = "whole"` is the same as declaring nothing: the whole of a secret is its payload.
* **A trailing newline is trimmed**, so a secret stored with one does not corrupt values such as `PGPASSWORD`.
* **A secret that does not exist is a missing key.** A `NotFound` is reported the same way a key absent from a YAML file is, alongside every other missing key in that project + version.
* **Fetches are concurrent and deduplicated.** Distinct secrets are fetched in parallel over one shared client, bounded by `SecretManagerConcurrency` (default 8) and by a whole-batch `SecretManagerTimeout` (default 30s). Vars pointing at the same project/secret/version cost one fetch.
* **Failures are reported together.** A batch with several bad secret ids names every one of them, in a stable order, rather than dying on the first.
* **`.enc` placement is advisory.** A GSM link resolves identically inside or outside an `.enc` block; putting it under `.enc` only signals that the value is sensitive.
* **`--no-enc`** skips GSM vars under `.enc`, like any other encrypted var.
* **`--no-decrypt` returns `[encrypted]`.** Unlike SOPS, there is no ciphertext form of a GSM secret. Thus, no fetch is done.

## `envsubst` cheatsheet:


| __Expression__                | __Meaning__                                                     |
| -----------------             | --------------                                                  |
| `${var}`                      | Value of `$var`
| `${var-${DEFAULT}}`           | If `$var` is not set, evaluate expression as `${DEFAULT}`
| `${var:-${DEFAULT}}`          | If `$var` is not set or is empty, evaluate expression as `${DEFAULT}`
| `${var=${DEFAULT}}`           | If `$var` is not set, evaluate expression as `${DEFAULT}`
| `${var:=${DEFAULT}}`          | If `$var` is not set or is empty, evaluate expression as `${DEFAULT}`
| `$$var`                       | Escape expressions. Result will be the string `$var`
| `${var^}`                     | Uppercase first character of `$var`
| `${var^^}`                    | Uppercase all characters in `$var`
| `${var,}`                     | Lowercase first character of `$var`
| `${var,,}`                    | Lowercase all characters in `$var`
| `${#var}`                     | String length of `$var`
| `${var:n}`                    | Offset `$var` `n` characters from start
| `${var: -n}`                  | Offset `$var` `n` characters from end
| `${var:n:len}`                | Offset `$var` `n` characters with max length of `len`
| `${var#pattern}`              | Strip shortest `pattern` match from start
| `${var##pattern}`             | Strip longest `pattern` match from start
| `${var%pattern}`              | Strip shortest `pattern` match from end
| `${var%%pattern}`             | Strip longest `pattern` match from end
| `${var/pattern/replacement}`  | Replace as few `pattern` matches as possible with `replacement`
| `${var//pattern/replacement}` | Replace as many `pattern` matches as possible with `replacement`
| `${var/#pattern/replacement}` | Replace `pattern` match with `replacement` from `$var` start
| `${var/%pattern/replacement}` | Replace `pattern` match with `replacement` from `$var` end


## Notes and references:

`envsubst` warning: make sure that any environmental substition declarations allow a file to be parsed as TOML without the usage of the `--envsubst` flag:
```toml
# valid envsubst definitions can be placed anywhere string values are valid
["${ENV}".vars]
thing = "${THING_VAR}"
# the `${ENV}` below creates a TOML read error
[env.vars]${ENV}
thing = "${THING_VAR}"
```

### Further references
* [TOML spec](https://toml.io/en/v1.0.0-rc.3#keyvalue-pair)
* [`yq` expressions](https://mikefarah.gitbook.io/yq/)
* [envsubst](https://www.gnu.org/software/bash/manual/html_node/Shell-Parameter-Expansion.html)

[sops]: https://github.com/mozilla/sops
[adc]: https://cloud.google.com/docs/authentication/application-default-credentials
