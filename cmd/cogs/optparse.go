package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Bestowinc/cogs"
)

// ----------------------
// CLI optparse functions
// ----------------------

func ifErr(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func getRawValue(cfgMap map[string]interface{}, keyList []string, delimiter string) (string, error) {
	var values []string
	// Interpret --sep='\n' and --sep='\t' as newlines and tabs
	switch delimiter {
	case "\\n":
		delimiter = "\n"
	case "\\t":
		delimiter = "\t"
	}
	if len(keyList) != 0 {
		for _, v := range keyList {
			keyName, ok := cfgMap[v]
			if !ok {
				return "", fmt.Errorf("getRawValue: key %s is missing from cfgMap", v)
			}
			values = append(values, fmt.Sprintf("%s", keyName))
		}
	} else {
		// iterate without an order reference if keyList is empty
		for _, v := range cfgMap {
			values = append(values, fmt.Sprintf("%s", v))
		}
	}
	return strings.Join(values, delimiter), nil

}

// modKeys should always return a flat associative array of strings
// coercing any interface{} value into a string
func modKeys(cfgMap map[string]interface{}, modFn ...func(string) string) map[string]string {
	newCfgMap := make(map[string]string)
	for k, v := range cfgMap {
		for _, fn := range modFn {
			k = fn(k)
		}
		newCfgMap[k] = fmt.Sprintf("%s", v)
	}
	return newCfgMap
}

// filterCfgMap retains only key names passed to --keys
func (c *Conf) filterCfgMap(cfgMap cogs.CfgMap) (cogs.CfgMap, error) {
	if cfgMap == nil {
		return nil, nil
	}

	// --not runs before --keys!
	// make sure to avoid --not=key_name --key=key_name, ya dingus!
	var notList []string
	if c.Not != "" {
		notList = strings.Split(c.Not, ",")
		cfgMap = cogs.Exclude(notList, cfgMap)
	}
	if c.Keys == "" {
		return cfgMap, nil
	}

	keyList := strings.Split(c.Keys, ",")
	newCfgMap := make(map[string]interface{})
	for _, key := range keyList {
		var ok bool
		if newCfgMap[key], ok = cfgMap[key]; !ok {
			hint := ""
			if cogs.InList(key, notList) {
				hint = fmt.Sprintf("\n\n--not=%s and --keys=%s were called\n"+
					"avoid trying to include and exclude the same value, ya dingus!", key, key)
			}
			if c.NoEnc {
				hint += "\n\n--no-enc was called: was it an encrypted value?\n"
			}
			return nil, fmt.Errorf("--key: [%s] missing from generated config%s", key, hint)
		}
	}
	return newCfgMap, nil
}

func (c *Conf) validate() (format cogs.Format, err error) {
	if !c.Gen {
		return "", nil
	}
	if format = cogs.Format(conf.Output); format.Validate() != nil {
		return "", fmt.Errorf("invalid opt: --out" + conf.Output)
	}

	switch {
	case format != cogs.Raw:
		if c.Delimiter != "" {
			return "", fmt.Errorf("invalid opt: --sep")
		}
	case format != cogs.Dotenv:
		if c.Export {
			return "", fmt.Errorf("invalid opt: --export")
		}
		if c.Preserve {
			return "", fmt.Errorf("invalid opt: --preserve")
		}
	}
	return format, nil
}
