package diagnostic_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/marcuoli/go-oracle-session/diagnostic"
	"github.com/marcuoli/go-oracle-session/rowset"
)

type failedCatalogQuery struct{}

func (failedCatalogQuery) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("ORA-00942: table or view APP.ORDER_LINES does not exist")
}

func ExampleStatementObjects() {
	// The statement and its verified references are selected together.
	statement := "SELECT * FROM APP.ORDERS JOIN APP.ORDER_LINES USING (ORDER_ID)"
	objects := []diagnostic.ObjectRef{
		{Schema: "APP", Name: "ORDERS", Kind: "table"},
		{Schema: "APP", Name: "ORDER_LINES", Kind: "view"},
	}
	_, queryErr := rowset.Query(context.Background(), failedCatalogQuery{}, statement)
	report, err := diagnostic.StatementObjects(queryErr, objects)
	if err != nil {
		fmt.Println(err)
	}
	for _, object := range report {
		objectName := object.Schema + "." + object.Name
		if object.ProblemCode != "" {
			fmt.Println(objectName, object.ProblemCode)
		} else {
			fmt.Println(objectName)
		}
	}
	// Output:
	// APP.ORDERS
	// APP.ORDER_LINES ORA-00942
}
