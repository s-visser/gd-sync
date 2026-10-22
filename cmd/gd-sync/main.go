// gd-sync keeps GoDaddy cPanel sites in Git, with the live server as the
// source of truth. See README.md.
package main

import (
	"os"

	"github.com/s-visser/gd-sync/internal/app"
)

func main() { os.Exit(app.Main(os.Args[1:])) }
