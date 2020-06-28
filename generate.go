package cogs

import (
	"fmt"
	"github.com/BurntSushi/toml"
)

type CogManifest struct {
	Name string
	Envs map[string]Env
}

type Env struct {
	Cfg map[string]Cfg
}

type Cfg struct {
	Path    string
	SubPath string
	Value   string
}

func Generate(env, cogFile string) error {
	var manifest CogManifest
	if _, err := toml.DecodeFile(cogFile, &manifest); err != nil {
		return err
	}
	fmt.Println(manifest.Envs)
	return nil
}

/*
{
  "name": "some_service",
  "env": {
    "docker": [
      {
        "cfg": {
          "other_var": {
            "value": "other_var_value"
          },
          "var": {
            "value": "var_value"
          }
        },
        "enc": {
          "enc_var": {
            "path": "path/to/file.enc.yaml"
          }
        }
      }
    ],
    "qa": [
      {
        "cfg": {
          "var": {
            "name": "VAR_NAME",
            "path": "path/to/qa/prefs.yaml",
            "sub_path": "config.key"
          }
        },
        "enc": {
          "enc_var": {
            "name": "ENC_VAR_NAME",
            "path": "path/to/qa/file.enc.yaml"
          }
        }
      }
    ]
  }
}
*/
