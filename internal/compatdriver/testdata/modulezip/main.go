// Command modulezip creates a standard Go module ZIP from an immutable Git
// revision for standalone downstream qualification. It never rewrites sources.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: modulezip repository revision version destination")
		os.Exit(2)
	}
	destination := os.Args[4]
	stage := destination + ".part"
	file, err := os.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	identity := module.Version{Path: "github.com/marcuoli/go-oracle-session", Version: os.Args[3]}
	err = modzip.CreateFromVCS(file, identity, os.Args[1], os.Args[2], "")
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(stage)
		panic(err)
	}
	checked, err := modzip.CheckZip(identity, stage)
	if err != nil {
		_ = os.Remove(stage)
		panic(err)
	}
	if err := os.Rename(stage, destination); err != nil {
		panic(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"version": identity.Version, "validFiles": len(checked.Valid), "invalidFiles": len(checked.Invalid), "omittedFiles": len(checked.Omitted)})
}
