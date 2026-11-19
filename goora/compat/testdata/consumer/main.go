package main

import (
	"database/sql"
	"fmt"

	"github.com/marcuoli/go-oracle-session/goora/compat"
	_ "github.com/sijms/go-ora/v3"
)

func main() {
	connector, err := compat.NewConnector("oracle://fixture:unused@127.0.0.1:1/fixture", compat.Options{})
	if err != nil {
		panic(err)
	}
	pool := sql.OpenDB(connector)
	defer pool.Close()
	if !compat.IsDriver(pool.Driver()) {
		panic("wrong compatibility factory")
	}
	fmt.Println("compatibility driver ready")
}
