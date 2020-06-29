package cogs

import (
	"fmt"

	"github.com/pelletier/go-toml"
)

type Cog struct {
	Name string
	// generate multiple envs?
	env Env
}

type Env struct {
	Config map[string]Cfg
}

type Cfg struct {
	// Defaults to key name unless explicitly declared
	Name  string
	Value string
	Path  string
	// default should be Cfg key name
	SubPath   string
	encrypted bool
}

func (c Cfg) GenerateValue() string {
	// if Path is empty or Value is non empty
	if c.Path == "" || c.Value != "" {
		return c.Value
	}

	if c.encrypted {
		// TODO COGS-1657
		// decrypt.File(c.Path, c.SubPath)
		return "|enc| " + c.Path
	}
	// TODO COGS-1659
	// cogs.File(c.Path, c.SubPath)
	return "path." + c.Path + "/" + c.SubPath

}

func (c Cfg) String() string {
	return fmt.Sprintf(`Cfg{
	Name: %s
	Value: %s
	Path: %s
	SubPath: %s
	encrypted: %t
}`, c.Name, c.Value, c.Path, c.SubPath, c.encrypted)
}

func (e Env) GenerateMap() map[string]string {
	cfgMap := make(map[string]string)
	for k, cfg := range e.Config {
		cfgMap[k] = cfg.GenerateValue()
	}
	return cfgMap

}

// Mapper is an iterface that defines a struct able to generate a flat associative array
type Mapper interface {
	GenerateMap() map[string]string
}

// Generate is a top level command that takes an env argument and cogfilepath to return a string map
func Generate(env, cogFile string) (map[string]string, error) {

	tree, err := toml.LoadFile(cogFile)
	if err != nil {
		return nil, err
	}
	return generate(env, tree)

}

func generate(env string, tree *toml.Tree) (map[string]string, error) {
	var cog Cog
	var ok bool
	var err error

	// grab manifest name
	cog.Name, ok = tree.Get("name").(string)
	if !ok || cog.Name == "" {
		return nil, fmt.Errorf("manifest.name string value must be present as a string")
	}

	var rawManifest map[string]interface{}
	if err = tree.Unmarshal(&rawManifest); err != nil {
		return nil, err
	}

	rawEnv, ok := rawManifest[env]
	if !ok {
		return nil, fmt.Errorf("%s environment missing from cog file", env)
	}

	cog.env.Config, err = ParseEnv(rawEnv)
	if err != nil {
		return nil, fmt.Errorf("%s: %s", env, err)
	}

	return cog.env.GenerateMap(), nil
}

// ParseEnv traverses an map interface to return a Cfg string map
func ParseEnv(rawMap interface{}) (cfgMap map[string]Cfg, err error) {
	mapEnv, ok := rawMap.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("env must be a table")
	}

	cfgMap = make(map[string]Cfg)

	// treat enc key as a nested cfgMap
	if rawEnc, ok := mapEnv["enc"]; ok {
		encMap, ok := rawEnc.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf(".enc must map to a table")
		}

		_, err := parseEnv(cfgMap, encMap)
		if err != nil {
			return nil, err
		}
		for key, cfg := range cfgMap {
			cfg.encrypted = true
			cfgMap[key] = cfg
		}
		// remove env map now that it is parsed
		delete(mapEnv, "enc")
	}
	return parseEnv(cfgMap, mapEnv)
}

func parseEnv(cfgMap map[string]Cfg, mapEnv map[string]interface{}) (map[string]Cfg, error) {
	var err error
	for k, rawCfg := range mapEnv {
		if _, ok := cfgMap[k]; ok {
			return nil, fmt.Errorf("%s: duplicate key present in env and env.enc", k)
		}
		switch t := rawCfg.(type) {
		case string:
			val := rawCfg.(string)
			cfgMap[k] = Cfg{
				Name:  k,
				Value: val,
			}
		case map[string]interface{}:
			val := rawCfg.(map[string]interface{})
			cfgMap[k], err = parseCfg(val)
			if err != nil {
				return nil, fmt.Errorf("%s: %s", k, err)
			}
		default:
			return nil, fmt.Errorf("%s: %s is an usupported type", k, t)
		}
	}
	return cfgMap, nil
}

func parseCfg(cfgVal map[string]interface{}) (Cfg, error) {
	var cfg Cfg
	var ok bool

	for k, v := range cfgVal {
		switch k {
		case "name":
			cfg.Name, ok = v.(string)
			if !ok {
				return cfg, fmt.Errorf(".name must be a string", k)
			}
		case "path":
			cfg.Path, ok = v.(string)
			if ok {
				continue
			}
			// cast to interface slice first since v.([]string) fails in one pass
			pathSlice, ok := v.([]interface{})
			if !ok {
				return cfg, fmt.Errorf("path must be a string or array of strings")
			}
			if len(pathSlice) != 2 {
				return cfg, fmt.Errorf("path array must only contain two values mapping to path and supbpath respectively")
			}
			cfg.Path, ok = pathSlice[0].(string)
			if !ok {
				return cfg, fmt.Errorf("path must be a string or array of strings")
			}

			cfg.SubPath, ok = pathSlice[1].(string)
			if !ok {
				return cfg, fmt.Errorf("path must be a string or array of strings")
			}

		default:
			return cfg, fmt.Errorf("%s is an unsupported key name", k)
		}
	}
	return cfg, nil
}
