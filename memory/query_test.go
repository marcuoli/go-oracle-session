package memory_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/marcuoli/go-oracle-session/memory"
)

func TestPDBStatementsBindScopeAndEffectiveValueGuards(t *testing.T) {
	for _, parameter := range []memory.Parameter{memory.SGA, memory.PGA} {
		t.Run(string(parameter), func(t *testing.T) {
			for _, options := range []memory.PDBOptions{{}, {AllowInherited: true}, {AllowInherited: true, EffectiveADB: true}} {
				statement, err := memory.PDBStatement(parameter, options)
				if err != nil {
					t.Fatal(err)
				}
				for _, guard := range []string{"SELECT ", "SOURCE_CONTAINER_ID", "CURRENT_CONTAINER_ID", "INSTANCE_ID", "SOURCE_ROWS", "POWER(1024,3)", "NLS_NUMERIC_CHARACTERS", "P.CON_ID=TO_NUMBER(SYS_CONTEXT('USERENV','CON_ID'))"} {
					if !strings.Contains(statement, guard) {
						t.Fatalf("missing %q", guard)
					}
				}
				inherited := strings.Contains(statement, "P.CON_ID IN (0,1)")
				if inherited != (options.AllowInherited && !options.EffectiveADB) {
					t.Fatal("inheritance option not enforced", options)
				}
				if options.EffectiveADB {
					for _, guard := range []string{"FROM V$PARAMETER", "ISSES_MODIFIABLE='FALSE'", "CLOUD_SERVICE", "'DWCS','OLTP','JDCS'"} {
						if !strings.Contains(statement, guard) {
							t.Fatalf("fallback lost %q", guard)
						}
					}
					if strings.Contains(statement, "'sga_max_size'") {
						t.Fatal("fallback used shared SGA maximum")
					}
				} else if !strings.Contains(statement, "V$SYSTEM_PARAMETER") || !strings.Contains(statement, "DENSE_RANK") {
					t.Fatal("primary lost system scope/selection")
				}
			}
		})
	}
}

func TestPDBStatementsRejectDynamicParameterNames(t *testing.T) {
	for _, parameter := range []memory.Parameter{"", "memory_target", "sga'; SELECT password FROM users"} {
		t.Run(string(parameter), func(t *testing.T) {
			statement, err := memory.PDBStatement(parameter, memory.PDBOptions{})
			if !errors.Is(err, memory.ErrUnsupportedParameter) || statement != "" {
				t.Fatal("dynamic parameter accepted", err)
			}
		})
	}
}
