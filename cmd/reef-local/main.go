package main

import (
	"flag"
	"fmt"
	"os"
	"reef/internal/local"
)

func main() {
	root := flag.String("root", ".", "Reef source directory")
	flag.Parse()
	action := "up"
	if flag.NArg() > 0 {
		action = flag.Arg(0)
	}
	launcher, e := local.New(*root)
	if e == nil {
		e = launcher.Run(action)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
