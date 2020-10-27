package cogs

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/drone/envsubst"
	"github.com/joho/godotenv"
	"github.com/mikefarah/yq/v3/pkg/yqlib"
	"gopkg.in/yaml.v3"
)

type readType string

const (
	rDotenv      readType = "dotenv"
	rJson        readType = "json"
	rJsonComplex readType = "json{}"
	rWhole       readType = "whole"
	deferred     readType = "" // defer file config type to filename suffix
)

// Validate ensures that a string is a valid readType enum
func (t readType) Validate() error {
	switch t {
	case rDotenv:
		return nil
	case rJson:
		return nil
	case rJsonComplex:
		return nil
	case rWhole:
		return nil
	default:
		return fmt.Errorf("%s is an invalid cfgType", string(t))
	}
}

func (t readType) String() string {
	switch t {
	case rDotenv:
		return string(rDotenv)
	case rJson:
		return string(rJson)
	case rJsonComplex:
		return "complex json"
	case rWhole:
		return "complex json"
	case deferred:
		return "deferred"
	default:
		return "unknown"
	}
}

// Queryable allows a query path to return the underlying value for a given visitor
type Queryable interface {
	SetValue(*Cfg) error
}

// readFile takes a filepath and returns the byte value of the data within
func readFile(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	stats, statsErr := file.Stat()
	if statsErr != nil {
		return nil, statsErr
	}

	var size int64 = stats.Size()
	bytes := make([]byte, size)

	_, err = file.Read(bytes)

	return bytes, nil

}

// envSubFile returns a file with environmental substitution applied, call tldr for more:
// $ tldr envsubst
func envSubFile(filePath string) (string, error) {
	bytes, err := readFile(filePath)
	if err != nil {
		return "", err
	}
	substEnv, err := envsubst.EvalEnv(string(bytes))
	if err != nil {
		return "", err
	}
	return substEnv, nil
}

// kindStr maps the yaml node types to strings for error messaging
var kindStr = map[yaml.Kind]string{
	0:                 "None",
	yaml.DocumentNode: "DocumentNode",
	yaml.SequenceNode: "SequenceNode",
	yaml.MappingNode:  "MappingNode",
	yaml.ScalarNode:   "ScalarNode",
	yaml.AliasNode:    "AliasNode",
}

// NewJsonVisitor returns a visitor object that satisfies the Queryable interface
// attempting to turn a supposed JSON byte slice into a *yaml.Node object
func NewJsonVisitor(buf []byte) (Queryable, error) {
	visitor := &yamlVisitor{
		rootNode:    &yaml.Node{},
		cachedNodes: make(map[string]map[string]string),
		parser:      yqlib.NewYqLib(),
	}

	tempMap := make(map[string]interface{})
	json.Unmarshal(buf, &tempMap)

	// deserialize to yaml.Node
	if err := visitor.rootNode.Encode(tempMap); err != nil {
		return nil, err
	}

	return visitor, nil
}

// NewYamlVisitor returns a visitor object that satisfies the Queryable interface
func NewYamlVisitor(buf []byte) (Queryable, error) {
	visitor := &yamlVisitor{
		rootNode:    &yaml.Node{},
		cachedNodes: make(map[string]map[string]string),
		parser:      yqlib.NewYqLib(),
	}

	// deserialize to yaml.Node
	if err := yaml.Unmarshal(buf, visitor.rootNode); err != nil {
		return nil, err
	}

	return visitor, nil
}

type yamlVisitor struct {
	rootNode    *yaml.Node
	cachedNodes map[string]map[string]string
	parser      yqlib.YqLib
}

// SetValue assigns the Value for a given Cfg using the existing Cfg.Path and Cfg.SubPath
func (n *yamlVisitor) SetValue(cfg *Cfg) (err error) {
	var ok bool

	// if readType is rWhole then decode the entire root node
	if cfg.readType == rWhole {
		if err = n.rootNode.Decode(&cfg.ComplexValue); err != nil {
			return err
		}
		return nil
	}

	if valMap, ok := n.cachedNodes[cfg.SubPath]; ok {
		cfg.Value, ok = valMap[cfg.Name]
		if !ok {
			return fmt.Errorf("unable to find %s", cfg)
		}
		return nil
	}

	node, err := n.get(cfg.SubPath)
	if err != nil {
		return err
	}

	// nodes with readType of deferred should be a string to string k/v pair
	if node.Kind != yaml.MappingNode && cfg.readType.Validate() != nil {
		return fmt.Errorf("%s: NodeKind/readType unsupported: %s/%s",
			cfg.Name, kindStr[node.Kind], cfg.readType)
	}

	cachedMap := make(map[string]string)

	switch cfg.readType {
	case rDotenv:
		cachedMap, err = visitDotenv(node)
		if err != nil {
			return err
		}
	case rJson:
		cachedMap, err = visitJson(node)
		if err != nil {
			return err
		}
	// do not cache complex maps for now
	case rJsonComplex:
		complexMap := make(map[string]interface{})
		err = node.Decode(&complexMap)
		if err != nil {
			return err
		}
		cfg.ComplexValue = complexMap
		return nil
	case deferred:
		err = node.Decode(&cachedMap)
		if err != nil {
			return err
		}
	}

	cfg.Value, ok = cachedMap[cfg.Name]
	if !ok {
		return fmt.Errorf("unable to find %s", cfg)
	}

	// cache the valid node before returning the desired value
	n.cachedNodes[cfg.SubPath] = cachedMap

	return nil

}

func (n *yamlVisitor) get(subPath string) (*yaml.Node, error) {
	nodeCtx, err := n.parser.Get(n.rootNode, subPath)
	if err != nil {
		return nil, err
	}
	// should only match a single node
	if len(nodeCtx) != 1 {
		return nil, fmt.Errorf("returned non singular result for path '%s'", subPath)
	}
	return nodeCtx[0].Node, nil
}

func visitDotenv(node *yaml.Node) (map[string]string, error) {
	var strEnv string

	if err := node.Decode(&strEnv); err != nil {
		var sliceEnv []string
		if err := node.Decode(&sliceEnv); err != nil {
			return nil, fmt.Errorf("Unable to decode node kind: %s to dotenv format", kindStr[node.Kind])
		}
		strEnv = strings.Join(sliceEnv, "\n")
	}
	envMap, err := godotenv.Unmarshal(strEnv)
	if err != nil {
		return nil, err
	}
	return envMap, nil
}

func visitJson(node *yaml.Node) (map[string]string, error) {
	var strEnv string

	if err := node.Decode(&strEnv); err != nil {
		var sliceEnv []string
		if err := node.Decode(&sliceEnv); err != nil {
			return nil, fmt.Errorf("Unable to decode node kind: %s to JSON format", kindStr[node.Kind])
		}
		strEnv = strings.Join(sliceEnv, "\n")
	}
	envMap := make(map[string]string)
	err := json.Unmarshal([]byte(strEnv), &envMap)
	if err != nil {
		return nil, err
	}
	return envMap, nil
}

// func visitJsonComplex(node *yaml.Node) (map[string]interface{}, error) {
//     var strEnv string

//     if err := node.Decode(&strEnv); err != nil {
//         return nil, fmt.Errorf("Unable to decode node kind: %s to complex JSON format", kindStr[node.Kind])
//     }
//     envMap := make(map[string]interface{})
//     err := json.Unmarshal([]byte(strEnv), &envMap)
//     if err != nil {
//         return nil, err
//     }
//     return envMap, nil
// }
