package main

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Bestowinc/cogs"
)

// allowUnexported whitelists private fields for cmp.Diff comparison
var allowUnexported cmp.Option = cmp.Exporter(func(reflect.Type) bool { return true })

type validateTestOut struct {
	name   string
	conf   Conf
	format cogs.Format
	err    error
}

func TestValidate(t *testing.T) {
	testCases := []validateTestOut{
		{
			name:   "NotGen",
			conf:   Conf{Gen: false},
			format: "",
		},
		{
			name: "InvalidFormat",
			conf: Conf{Gen: true, Output: "nope"},
			err:  errors.New("invalid opt: --out nope"),
		},
		{
			name:   "ComposeFormat",
			conf:   Conf{Gen: true, Output: "compose"},
			format: cogs.Compose,
		},
		{
			name:   "SepWithRaw",
			conf:   Conf{Gen: true, Output: "raw", Delimiter: ","},
			format: cogs.Raw,
		},
		{
			name: "SepWithDotenv",
			conf: Conf{Gen: true, Output: "dotenv", Delimiter: ","},
			err:  errors.New("invalid opt: --sep"),
		},
		{
			name: "SepWithCompose",
			conf: Conf{Gen: true, Output: "compose", Delimiter: ","},
			err:  errors.New("invalid opt: --sep"),
		},
		{
			name:   "ExportWithDotenv",
			conf:   Conf{Gen: true, Output: "dotenv", Export: true},
			format: cogs.Dotenv,
		},
		{
			name: "ExportWithCompose",
			conf: Conf{Gen: true, Output: "compose", Export: true},
			err:  errors.New("invalid opt: --export"),
		},
		{
			name: "ExportWithJSON",
			conf: Conf{Gen: true, Output: "json", Export: true},
			err:  errors.New("invalid opt: --export"),
		},
		{
			name:   "PreserveWithDotenv",
			conf:   Conf{Gen: true, Output: "dotenv", Preserve: true},
			format: cogs.Dotenv,
		},
		{
			name:   "PreserveWithCompose",
			conf:   Conf{Gen: true, Output: "compose", Preserve: true},
			format: cogs.Compose,
		},
		{
			name: "PreserveWithYAML",
			conf: Conf{Gen: true, Output: "yaml", Preserve: true},
			err:  errors.New("invalid opt: --preserve"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// validate() reads the package level conf for --out
			conf = tc.conf
			format, err := tc.conf.validate()
			if diff := cmp.Diff(fmt.Errorf("%s", tc.err), fmt.Errorf("%s", err), allowUnexported); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
			if tc.err != nil {
				return
			}
			if diff := cmp.Diff(tc.format, format); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}
