package main

import (
	"fmt"
	"log"

	"github.com/marcuoli/go-oracle-session/memory"
)

func main() {
	statement, err := memory.PDBStatement(memory.SGA, memory.PDBOptions{AllowInherited: true})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(statement)
	// An admitted session executes this SELECT. DecodePDB validates its result;
	// endpoint identity, timeout and missing/NULL fallback remain caller-owned.
}
