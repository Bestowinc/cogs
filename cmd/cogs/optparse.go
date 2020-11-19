package main

import (
	"fmt"
	"os"
	"strings"
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

func getRawValue(cfgMap map[string]interface{}, delimiter string) string {
	var values []string
	// for now, no delimiter
	for _, v := range cfgMap {
		values = append(values, fmt.Sprintf("%s", v))
	}
	return strings.Join(values, delimiter)

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

// exclude produces a laundered map with exclusionList values missing
func exclude(exclusionList []string, cfgMap map[string]interface{}) map[string]interface{} {
	newCfgMap := make(map[string]interface{})
	for k := range cfgMap {
		if inList(k, exclusionList) {
			continue
		}
		newCfgMap[k] = cfgMap[k]
	}
	return newCfgMap
}

func inList(s string, ss []string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
