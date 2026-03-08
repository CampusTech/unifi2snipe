package main

import "github.com/CampusTech/unifi2snipe/cmd"

var version = "dev"

func main() {
	cmd.Version = version
	cmd.Execute()
}
