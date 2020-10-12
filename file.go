package cogs

import (
	"fmt"
	"github.com/mikefarah/yq/v3/pkg/yqlib"
	"gopkg.in/yaml.v3"
	"io"
)

type readType string

const (
	dotenv   readType = "dotenv"
	deferred readType = "" // defer file config type to filename suffix
)

// Validate ensures that a string is a valid readType enum
func (t readType) Validate() error {
	switch t {
	case dotenv:
		return nil
	default:
		return fmt.Errorf("%s is an invalid cfgType", t)
	}
}

type Queryable interface {
	Get(queryPath string) (string, error)
}

// NewYamlVisitor returns a visitor object that satisfies the Queryable interface
func NewYamlVisitor(r io.Reader) (*yamlVisitor, error) {
	visitor := &yamlVisitor{}
	buf := []byte{}

	// read to buffer
	if _, err := r.Read(buf); err != nil {
		return nil, err
	}

	// deserialize to yaml.Node
	if err := yaml.Unmarshal(buf, &visitor.rootNode); err != nil {
		return nil, err
	}

	visitor.parser = yqlib.NewYqLib()

	return visitor, nil
}

type cachedNode struct {
	readType readType
}
type yamlVisitor struct {
	rootNode    *yaml.Node
	cachedNodes map[string]map[string]string
	parser      yqlib.YqLib
}

func (n *yamlVisitor) Get(cfg Cfg) (err error) {
	var ok bool

	if cfg.SubPath == "" {
		nodeCtx, err := n.get(cfg.Name)
		if err != nil {
			return err
		}
		// a top level value should be a string to string k/v pair
		if len(nodeCtx) != 1 {
			return fmt.Errorf("returned non signular result for %s", cfg)
		}
		err = nodeCtx[0].Node.Decode(&cfg.Value)
		if err != nil {
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

	nodeCtx, err := n.get(cfg.SubPath)
	if err != nil {
		return err
	}

	// should only match a single node
	if len(nodeCtx) != 1 {
		return fmt.Errorf("returned non signular result for %s", cfg)
	}

	node := nodeCtx[0].Node

	// nodes with readType of deferred should be a string to string k/v pair
	if node.Kind != yaml.MappingNode || cfg.readType != deferred {
		return fmt.Errorf("Node kind unsupported at this time: %s", kindStr[node.Kind])
	}

	// for now only support string maps
	// TODO handle dotenv readType
	cachedMap := make(map[string]string)
	err = node.Decode(&cachedMap)
	if err != nil {
		return err
	}

	cfg.Value, ok = cachedMap[cfg.Name]
	if !ok {
		return fmt.Errorf("unable to find %s", cfg)
	}

	// cache the valid node before returning the desired value
	n.cachedNodes[cfg.SubPath] = cachedMap

	return nil

}

func (n *yamlVisitor) get(path string) ([]*yqlib.NodeContext, error) {
	return n.parser.Get(n.rootNode, path)
}

var kindStr = map[yaml.Kind]string{
	yaml.DocumentNode: "DocumentNode",
	yaml.SequenceNode: "SequenceNode",
	yaml.MappingNode:  "MappingNode",
	yaml.ScalarNode:   "ScalarNode",
	yaml.AliasNode:    "AliasNode",
}
