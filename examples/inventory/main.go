// Command inventory demonstrates synthetic parsing without database access.
package main

import (
	"fmt"
	"github.com/marcuoli/go-oracle-session/inventory"
	"os"
)

func main() {
	entries, err := inventory.Parse([]byte("name,connect,username,password\nexample,db.example.invalid:1521/service,reader,synthetic\n"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Report safe identities only; credentials remain caller-owned.
	fmt.Printf("Admitted %d endpoint: %s\n", len(entries), entries[0].Name)
}
