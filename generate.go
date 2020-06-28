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
