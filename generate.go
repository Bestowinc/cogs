package cogs

import (
	"fmt"
	"github.com/pelletier/go-toml"
)

type Cog struct {
	Name string
	envs map[string]Env
}

type Env struct {
	Config map[string]Cfg
	Enc    map[string]Cfg `toml:"enc"`
}

type Cfg struct {
	Path    string
	SubPath string
	Value   string
}

func Generate(env, cogFile string) error {
	// var manifest map[string]interface{}
	var cog Cog
	var ok bool

	tree, err := toml.LoadFile(cogFile)
	if err != nil {
		return err
	}

	// grab manifest name
	cog.Name, ok = tree.Get("name").(string)
	if !ok || cog.Name == "" {
		return fmt.Errorf("manifest.name string value must be present as a string")
	}
	// prep tree for unmarshaling
	tree.Delete("name")

	if err = tree.Unmarshal(&cog.envs); err != nil {
		return err
	}
	fmt.Printf("%v\n", cog)
	fmt.Println(cog.Name)
	return nil
}

// Validate ensures the given toml.Tree does not contain reserved namespaces
// func Validate(tree *toml.Tree) error {
//     for _, k := tree.Keys()

// }
