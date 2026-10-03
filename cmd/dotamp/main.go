package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("dotamp", version)
		return
	}
	fmt.Fprintln(os.Stderr, "dotamp: not wired yet")
	os.Exit(1)
}
