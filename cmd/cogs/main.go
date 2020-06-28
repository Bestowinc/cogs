package main

import (
	"fmt"
	"os"

	"github.com/bestowinc/cogs"
	"github.com/docopt/docopt-go"
)

// var cmdMap = map[string]func(cog{
//     "generate": generateCmd,
// }

func main() {
	usage := `Usage:
  example generate <env> <cog-file>`

	opts, _ := docopt.ParseArgs(usage, os.Args[1:], "0.1")
	var conf struct {
		Generate bool
		Env      string
		File     string `docopt:"<cog-file>"`
	}

	opts.Bind(&conf)
	_ = fmt.Println
	switch {
	case conf.Generate:
		if err := cogs.Generate(conf.Env, conf.File); err != nil {
			panic(err)
		}
	}
}
