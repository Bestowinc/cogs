package cogs

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml"
)

// used to represent Cfg k/v pair at the top level of a file
const noSubPath = ""

// Cfg holds all the data needed to generate one string key value pair
type Cfg struct {
	// Defaults to key name unless explicitly declared
	Name  string
	Value string
	Path  string
	// default should be Cfg key name
	SubPath   string
	encrypted bool
	readType  readType
}

// String holds the string representation of a Cfg struct
func (c Cfg) String() string {
	return fmt.Sprintf(`Cfg{
	Name: %s
	Value: %s
	Path: %s
	SubPath: %s
	encrypted: %t
}`, c.Name, c.Value, c.Path, c.SubPath, c.encrypted)
}

type configMap map[string]Cfg

// Mapper is meant to define an object that returns the final string map to be used in a configuration
type Mapper interface {
	ResolveMap(RawEnv) (map[string]string, error)
	SetName(string)
}

// Gear represents one of the envs in a cog manifest.
// The term "gear" is used to refer to the operating state of a machine (similar
// to how a microservice can operate locally or in a remote environment)
// rather than a gear object. The term "switching gears" is an apt representation
// of how one Cog manifest file can have many environments
type Gear struct {
	Name   string
	cfgMap configMap
}

// SetName sets the gear name to the provided string
func (g *Gear) SetName(name string) {
	g.Name = name
}

// ResolveMap outputs the flat associative string, resolving potential filepath pointers
// held by Cfg objects by calling the .ResolveValue() method
func (g *Gear) ResolveMap(env RawEnv) (map[string]string, error) {
	var err error

	g.cfgMap, err = parseEnv(env)
	if err != nil {
		return nil, err
	}

	// includes Cfg objects with a direct file and an empty SubPath:
	// ex: var.path = "./path"
	// ---
	// as well as Cfg objects with SubPaths present:
	// ex: var.path = ["./path", ".subpath"]
	// ---
	pathGroup := make(map[string][]Cfg)

	// 1. sort Cfgs by Path
	for _, cfg := range g.cfgMap {
		if cfg.Path != "" && !cfg.encrypted {
			if _, ok := pathGroup[cfg.Path]; !ok {
				pathGroup[cfg.Path] = []Cfg{}
			}
			pathGroup[cfg.Path] = append(pathGroup[cfg.Path], cfg)
		}
		// TODO COGS-1657
		if cfg.encrypted {
			cfg.Value = "|enc|" + cfg.Path
		}
	}

	for path, cfgs := range pathGroup {
		// 2. for each distinct Path: generate a Reader object
		fileReader, err := os.Open(path)
		if err != nil {
			return nil, err
		}

		// 3. create yaml visitor to handle SubPath strings
		visitor, err := NewYamlVisitor(fileReader)
		if err != nil {
			return nil, err
		}

		// 4. traverse every Path and possible SubPath retrieving the Cfg.Values associated with it
		for _, cfg := range cfgs {
			err := visitor.Get(cfg)
			if err != nil {
				return nil, err
			}

		}
	}

	// final output
	cfgOut := make(map[string]string)
	for _, cfg := range g.cfgMap {
		cfgOut[cfg.Name] = cfg.Value
	}

	return cfgOut, nil

}

// RawEnv is meant to represent the topmost untraversed level of a cog environment
type RawEnv map[string]interface{}

// Generate is a top level command that takes an env argument and cogfilepath to return a string map
func Generate(envName, cogFile string) (map[string]string, error) {

	tree, err := toml.LoadFile(cogFile)
	if err != nil {
		return nil, err
	}
	return generate(envName, tree, &Gear{})

}

func generate(envName string, tree *toml.Tree, gear Mapper) (map[string]string, error) {
	var ok bool
	var err error

	type rawManifest struct {
		table map[string]RawEnv
	}

	// grab manifest name
	name, ok := tree.Get("name").(string)
	if !ok || name == "" {
		return nil, fmt.Errorf("manifest.name string value must be present as a string")
	}
	tree.Delete("name")

	gear.SetName(name)

	var manifest rawManifest
	if err = tree.Unmarshal(&manifest.table); err != nil {
		return nil, err
	}

	env, ok := manifest.table[envName]
	if !ok {
		return nil, fmt.Errorf("%s environment missing from cog file", envName)
	}

	genOut, err := gear.ResolveMap(env)
	if err != nil {
		return nil, fmt.Errorf("%s: %s", envName, err)
	}

	return genOut, nil
}

// parseEnv traverses an map interface to populate a gear's configMap
func parseEnv(env RawEnv) (cfgMap configMap, err error) {
	cfgMap = make(configMap)

	err = decodeEncrypted(cfgMap, env)
	if err != nil {
		return nil, err
	}

	err = decodeEnv(cfgMap, env)
	if err != nil {
		return nil, err
	}
	return cfgMap, nil
}

func decodeEnv(cfgMap configMap, env RawEnv) error {
	var err error

	for k, rawCfg := range env {
		if _, ok := cfgMap[k]; ok {
			return fmt.Errorf("%s: duplicate key present in env and env.enc", k)
		}
		switch t := rawCfg.(type) {
		case string:
			cfgMap[k] = Cfg{
				Name:  k,
				Value: t,
			}
		case map[string]interface{}:
			cfgMap[k], err = parseCfgMap(t)
			if err != nil {
				return fmt.Errorf("%s: %s", k, err)
			}
		default:
			return fmt.Errorf("%s: %s is an unsupported type", k, t)
		}
	}
	return nil
}

func decodeEncrypted(cfgMap configMap, env RawEnv) error {
	// treat enc key as a nested configMap
	enc, ok := env["enc"]
	if !ok {
		return nil
	}
	rawEnc, ok := enc.(map[string]interface{})
	if !ok {
		return fmt.Errorf(".enc must map to a table")
	}

	// parse through encrypted variables first
	err := decodeEnv(cfgMap, rawEnc)
	if err != nil {
		return err
	}
	for key, cfg := range cfgMap {
		cfg.encrypted = true
		cfgMap[key] = cfg
	}
	// remove env map now that it is parsed
	delete(env, "enc")

	return nil
}

// parseCfg handles the cases when a config value maps to a non string object type
func parseCfgMap(cfgVal map[string]interface{}) (Cfg, error) {
	var cfg Cfg
	var ok bool

	for k, v := range cfgVal {
		switch k {
		case "name":
			cfg.Name, ok = v.(string)
			if !ok {
				return cfg, fmt.Errorf(".name must be a string")
			}
		case "path":
			// a path key can map to two valid types:
			// 1. path value is a single string mapping to filepath
			// 2. path value  is a two index slice mapping to [filepath, subpath] respectively

			// singular filepath string
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
				return cfg, fmt.Errorf("path array must only contain two values mapping to path and subpath respectively")
			}
			// filepath string
			cfg.Path, ok = pathSlice[0].(string)
			if !ok {
				return cfg, fmt.Errorf("path must be a string or array of strings")
			}

			// subpath string index used to traverse the data object once deserialized
			cfg.SubPath, ok = pathSlice[1].(string)
			if !ok {
				return cfg, fmt.Errorf("path must be a string or array of strings")
			}
		case "type":
			cfg.readType = readType(k)
			if err := cfg.readType.Validate(); err != nil {
				return cfg, err
			}

		default:
			return cfg, fmt.Errorf("%s is an unsupported key name", k)
		}
		if _, ok := cfgVal["type"]; ok {
			cfg.readType = deferred
		}

	}
	return cfg, nil
}
