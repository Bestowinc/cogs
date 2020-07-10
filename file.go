package cogs

import "fmt"

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
