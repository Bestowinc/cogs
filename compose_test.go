package cogs

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type composeTestOut struct {
	name   string
	envMap map[string]string
	output string
	err    error
}

func TestMarshalCompose(t *testing.T) {
	testCases := []composeTestOut{
		{
			name: "SimpleValues",
			envMap: map[string]string{
				"STR":   "value",
				"INT":   "123",
				"FLOAT": "1.5",
				"BOOL":  "true",
				"EMPTY": "",
			},
			output: "BOOL=true\nEMPTY=\nFLOAT=1.5\nINT=123\nSTR=value",
		},
		{
			name: "NoQuotingOrEscaping",
			envMap: map[string]string{
				"DQUOTE":   `"quoted"`,
				"SQUOTE":   `'quoted'`,
				"DOLLAR":   "$FOO",
				"BRACE":    "${FOO}",
				"HASH":     "a # b",
				"EQUALS":   "a=b",
				"SPACES":   "a b c",
				"TRAILING": "a ",
				"BSLASH":   `a\nb`,
				"BANG":     "a!b",
			},
			output: "BANG=a!b\n" +
				`BRACE=${FOO}` + "\n" +
				`BSLASH=a\nb` + "\n" +
				"DOLLAR=$FOO\n" +
				`DQUOTE="quoted"` + "\n" +
				"EQUALS=a=b\n" +
				"HASH=a # b\n" +
				"SPACES=a b c\n" +
				`SQUOTE='quoted'` + "\n" +
				"TRAILING=a ",
		},
		{
			name:   "Empty",
			envMap: map[string]string{},
			output: "",
		},
		{
			name:   "SortOrder",
			envMap: map[string]string{"b": "2", "A": "1", "a": "3", "_c": "4"},
			output: "A=1\n_c=4\na=3\nb=2",
		},
		{
			name:   "NewlineValue",
			envMap: map[string]string{"MULTI": "a\nb"},
			err:    errors.New("MarshalCompose: key MULTI: value contains \"\\n\", which a docker env-file cannot represent"),
		},
		{
			name:   "CarriageReturnValue",
			envMap: map[string]string{"MULTI": "a\rb"},
			err:    errors.New("MarshalCompose: key MULTI: value contains \"\\r\", which a docker env-file cannot represent"),
		},
		{
			name:   "NulValue",
			envMap: map[string]string{"NUL": "a\x00b"},
			err:    errors.New("MarshalCompose: key NUL: value contains \"\\x00\", which a docker env-file cannot represent"),
		},
		{
			name:   "KeyWithEquals",
			envMap: map[string]string{"FOO=BAR": "1"},
			err:    errors.New(`MarshalCompose: invalid key name: "FOO=BAR"`),
		},
		{
			name:   "KeyWithSpace",
			envMap: map[string]string{"FOO BAR": "1"},
			err:    errors.New(`MarshalCompose: invalid key name: "FOO BAR"`),
		},
		{
			name:   "KeyWithLeadingDigit",
			envMap: map[string]string{"1FOO": "1"},
			err:    errors.New(`MarshalCompose: invalid key name: "1FOO"`),
		},
		{
			name:   "EmptyKey",
			envMap: map[string]string{"": "1"},
			err:    errors.New(`MarshalCompose: invalid key name: ""`),
		},
		{
			name:   "ExportedKey",
			envMap: map[string]string{"export FOO": "1"},
			err:    errors.New(`MarshalCompose: invalid key name: "export FOO"`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			output, err := MarshalCompose(tc.envMap)
			if diff := cmp.Diff(fmt.Errorf("%s", tc.err), fmt.Errorf("%s", err), AllowUnexported); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
			if tc.err != nil {
				return
			}
			if diff := cmp.Diff(tc.output, output); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}
