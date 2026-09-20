package main

import (
	"log"

	cnc "github.com/fahrel/cnc"
)

func main() {
	cli := cnc.NewCLI()
	if err := cli.Execute(); err != nil {
		log.Fatal(err)
	}
}
