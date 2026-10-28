package main

import (
	"flag"
	"fmt"
	"os"
	"reef/internal/local"
)

func main() {
	root := flag.String("root", ".", "Reef source directory")
	lan := flag.Bool("lan", false, "Allow access from other computers (IPv4)")
	loopback := flag.Bool("local", false, "Restrict access to this machine")
	flag.Parse()
	action := "up"
	if flag.NArg() > 0 {
		action = flag.Arg(0)
	}
	options := []string{}
	if *lan {
		options = append(options, "--lan")
	}
	if *loopback {
		options = append(options, "--local")
	}
	if flag.NArg() > 1 {
		options = append(options, flag.Args()[1:]...)
	}
	launcher, e := local.New(*root)
	if e == nil {
		e = launcher.Run(action, options...)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
