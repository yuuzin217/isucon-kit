package main

import (
	"os"

	"github.com/yuuzin217/isucon-kit/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
