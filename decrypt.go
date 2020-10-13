package cogs

import (
    "fmt"
    "go.mozilla.org/sops/v3/decrypt"
    "gopkg.in/yaml.v3"
)

func decryptFile(path string) (map[string]string, error) {
    yamlMap := make(map[string]string)
    sec, err := decrypt.File(path, "yaml")
    if err != nil {
        return nil, fmt.Errorf("cannot decrypt file: %s", err)
    }
    err = yaml.Unmarshal(sec, &yamlMap)
    if err != nil {
        return nil, fmt.Errorf("cannot unmarshal file: %s", err)
    }
    return yamlMap, nil
}
