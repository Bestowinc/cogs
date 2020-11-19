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
	// read format overrides
	rDotenv      readType = "dotenv"
	rJSON        readType = "json"
	rJSONComplex readType = "json{}" // complex json key value pair: {"k":{"v1":[],"v2":[]}}

	// read format derived from filepath suffix
	rWhole   readType = "whole" // indicates to associate the entirety of a file to the given key name
	deferred readType = ""      // defer file config type to filename suffix
)

// Validate ensures that a string is a valid readType enum
func (t readType) Validate() error {
	switch t {
	case rDotenv, rJSON, rJSONComplex, rWhole:
		return nil
	default: // deferred readType should not be validated
		return fmt.Errorf("%s is an invalid cfgType", string(t))
	}
}

func (t readType) String() string {
	switch t {
	case rDotenv:
		return string(rDotenv)
	case rJSON:
		return "flat json"
	case rJSONComplex:
		return "complex json"
	case rWhole:
		return "whole file"
	case deferred:
		return "deferred"
	default:
		return "unknown"
	}
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

	if _, err = file.Read(bytes); err != nil {
		return nil, err
	}

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

// Visitor allows a query path to return the underlying value for a given visitor
type Visitor interface {
	SetValue(*Cfg) error
}

// NewJSONVisitor returns a visitor object that satisfies the Visitor interface
// attempting to turn a supposed JSON byte slice into a *yaml.Node object
func NewJSONVisitor(buf []byte) (Visitor, error) {
	tempMap := make(map[string]interface{})
	if err := json.Unmarshal(buf, &tempMap); err != nil {
		return nil, err
	}
	// deserialize to yaml.Node
	rootNode := &yaml.Node{}
	if err := rootNode.Encode(tempMap); err != nil {
		return nil, err
	}
	return newVisitor(rootNode), nil
}

// NewYamlVisitor returns a visitor object that satisfies the Visitor interface
func NewYamlVisitor(buf []byte) (Visitor, error) {
	// deserialize to yaml.Node
	rootNode := &yaml.Node{}
	if err := yaml.Unmarshal(buf, rootNode); err != nil {
		return nil, err
	}
	return newVisitor(rootNode), nil
}

func newVisitor(node *yaml.Node) Visitor {
	return &visitor{
		rootNode:       node,
		visited:        make(map[string]map[string]string),
		visitedComplex: make(map[string]interface{}),
		parser:         yqlib.NewYqLib(),
	}
}

type visitor struct {
	rootNode       *yaml.Node
	visited        map[string]map[string]string
	visitedComplex map[string]interface{}
	parser         yqlib.YqLib
}

// SetValue assigns the Value for a given Cfg using the existing Cfg.Path and Cfg.SubPath
func (n *visitor) SetValue(cfg *Cfg) (err error) {
	// rWhole readType grabs the entire rootNode and assigns cfg.ComplexValue to it
	if cfg.readType == rWhole || cfg.readType == rJSONComplex {
		return n.visitComplex(cfg)
	}

	// check if cfg.SubPath value has been used in a previous SetValue call
	if flatMap, ok := n.visited[cfg.SubPath]; ok {
		if cfg.Value, ok = flatMap[cfg.Name]; !ok {
			return fmt.Errorf("unable to find %s", cfg.Name)
		}
		return nil
	}

	// if SubPath is an empty string, grab the top level value that corresponds
	// to a key with the string value of cfg.Name and attempt to assign it
	// to cfg.Value by calling node.Decode
	node, err := n.get(cfg.SubPath)
	if err != nil {
		return err
	}

	if cfg.readType == rJSONComplex {
		return nil
	}

	if node.Kind != yaml.MappingNode && cfg.readType.Validate() != nil {
		return fmt.Errorf("%s: NodeKind/readType unsupported: %s/%s",
			cfg.Name, kindStr[node.Kind], cfg.readType)
	}

	cachedMap := make(map[string]string)

	switch cfg.readType {
	case rDotenv:
		// .(map[string]interface{})
		err = visitDotenv(cachedMap, node)
	case rJSON:
		err = visitJSON(cachedMap, node)
	case deferred:
		err = node.Decode(&cachedMap)
		// cfg.ComplexValue = complexMap
		// return nil
	default:
		err = fmt.Errorf("unsupported readType: %s", cfg.readType)
	}
	if err != nil {
		return err
	}

	// cache the valid node before returning the desired value
	n.visited[cfg.SubPath] = cachedMap

	return n.SetValue(cfg)

}

// visitComplex handles the rWhole and rJSONComplex read types
func (n *visitor) visitComplex(cfg *Cfg) (err error) {
	var ok bool
	// check if cfg.SubPath and readType has been used before
	// since there is no guarantee that cfg.SubPath resolves to a flat map,
	// there is no reason to nest maps within each other
	key := cfg.SubPath + cfg.readType.String()
	if cfg.ComplexValue, ok = n.visitedComplex[key]; ok {
		return nil
	}

	println(key)

	switch cfg.readType {
	case rWhole:
		err = n.rootNode.Decode(&cfg.ComplexValue)
	case rJSONComplex:
		var node *yaml.Node
		// 1. grab the yaml node corresponding to the subpath
		fmt.Println("rootNode: ", n.rootNode)
		node, err = n.get(cfg.SubPath)
		if err != nil {
			return err
		}
		fmt.Println("getNode: ", node)
		// 2. decode to cfg.ComplexValue

		err = visitJSONComplex(cfg.ComplexValue, node)
	default:
		err = fmt.Errorf("unsupported readType: %s", cfg.readType)
	}
	if err != nil {
		return err
	}

	// cache the already decoded cfg.ComplexValue to visitor.visitedComplex
	// rWhole should have a SubPath of ""
	n.visitedComplex[key] = cfg.ComplexValue

	return nil
}

func (n *visitor) get(subPath string) (*yaml.Node, error) {
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

func visitDotenv(cache map[string]string, node *yaml.Node) error {
	var strEnv string

	if err := node.Decode(&strEnv); err != nil {
		var sliceEnv []string
		if err := node.Decode(&sliceEnv); err != nil {
			return fmt.Errorf("Unable to decode node kind: %s to dotenv format", kindStr[node.Kind])
		}
		strEnv = strings.Join(sliceEnv, "\n")
	}
	return godotenv.Write(cache, strEnv)
}

func visitJSON(cache map[string]string, node *yaml.Node) error {
	var strEnv string

	if err := node.Decode(&strEnv); err != nil {
		var sliceEnv []string
		if err := node.Decode(&sliceEnv); err != nil {
			return fmt.Errorf("Unable to decode node kind: %s to flat JSON format", kindStr[node.Kind])
		}
		strEnv = strings.Join(sliceEnv, "\n")
	}
	return json.Unmarshal([]byte(strEnv), &cache)
}

func visitJSONComplex(cache interface{}, node *yaml.Node) error {
	fmt.Printf("%+v\n", node.Content)
	b, err := yaml.Marshal(node.Content)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &cache)
}
