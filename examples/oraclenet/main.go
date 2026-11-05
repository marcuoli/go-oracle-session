// Command oraclenet resolves local connection configuration without dialing.
package main

import (
	"fmt"
	"github.com/marcuoli/go-oracle-session/oraclenet"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: oraclenet <network-directory> <alias>")
		os.Exit(2)
	}
	descriptor, err := oraclenet.ResolveAlias(os.Args[2], os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(descriptor)
}
