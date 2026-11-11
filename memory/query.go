// Package memory selects and decodes read-only configured-memory facts.
// It does not open sessions, authorize scopes, evaluate readiness or aggregate RAC.
package memory

import "errors"

// Parameter selects a fixed configured-memory parameter family, never arbitrary SQL.
type Parameter string

// SGA and PGA select configured SGA size and aggregate PGA target respectively.
const (
	SGA Parameter = "sga"
	PGA Parameter = "pga"
)

// ErrUnsupportedParameter rejects values outside the fixed parameter families.
var ErrUnsupportedParameter = errors.New("unsupported configured-memory parameter")

// PDBOptions declares caller-admitted inheritance and effective-value selection.
// Callers must establish Oracle 12+ and an admitted connected PDB. EffectiveADB
// additionally requires Oracle 18+ and a primary NULL/no-row result; SQL guards
// cloud-service identity and non-session-modifiable values. Errors/invalid rows
// are not fallback triggers. This package never changes sessions or containers.
type PDBOptions struct {
	AllowInherited bool
	EffectiveADB   bool
}

// PDBStatement returns a current-instance, connected-PDB SELECT with provenance.
// SGA prefers nonzero PDB SGA_TARGET; explicit inheritance permits visible CDB
// SGA_MAX_SIZE. PGA prefers its own aggregate target, including a known zero.
// EffectiveADB selects only the current container. DecodePDB must validate the
// result before use. Values are converted from bytes to three-decimal GiB by
// Oracle, with NLS-independent exact text. This is not observed consumption.
func PDBStatement(parameter Parameter, options PDBOptions) (string, error) {
	child, parent := "pga_aggregate_target", "pga_aggregate_target"
	priority := "P.CON_ID=TO_NUMBER(SYS_CONTEXT('USERENV','CON_ID'))"
	selection := ""
	switch parameter {
	case SGA:
		child, parent = "sga_target", "sga_max_size"
		priority += " AND (P.VALUE IS NULL OR TO_NUMBER(P.VALUE)<>0)"
		selection = " AND (SOURCE_CONTAINER_ID<>TO_NUMBER(SYS_CONTEXT('USERENV','CON_ID')) OR VALUE IS NULL OR TO_NUMBER(VALUE)<>0)"
	case PGA:
	default:
		return "", ErrUnsupportedParameter
	}
	projection := `SELECT ` + numeric("ROUND(TO_NUMBER(VALUE)/POWER(1024,3),3)") + ` AS VALUE, ` + numeric("SOURCE_CONTAINER_ID") + ` AS SOURCE_CONTAINER_ID, ` + numeric("TO_NUMBER(SYS_CONTEXT('USERENV','CON_ID'))") + ` AS CURRENT_CONTAINER_ID, PARAMETER_NAME, ` + numeric("SOURCE_ROWS") + ` AS SOURCE_ROWS, ` + numeric("TO_NUMBER(SYS_CONTEXT('USERENV','INSTANCE'))") + ` AS INSTANCE_ID FROM (`
	inherited := ""
	if options.AllowInherited {
		inherited = " OR (P.CON_ID IN (0,1) AND P.NAME='" + parent + "')"
	}
	query := projection + `SELECT P.VALUE, P.NAME AS PARAMETER_NAME, P.CON_ID AS SOURCE_CONTAINER_ID,
 COUNT(*) OVER (PARTITION BY P.CON_ID,P.NAME) AS SOURCE_ROWS,
 DENSE_RANK() OVER (ORDER BY CASE WHEN ` + priority + ` THEN 0 WHEN P.CON_ID=0 THEN 1 WHEN P.CON_ID=1 THEN 2 ELSE 3 END) AS SOURCE_PRIORITY
 FROM V$SYSTEM_PARAMETER P
 WHERE (P.CON_ID=TO_NUMBER(SYS_CONTEXT('USERENV','CON_ID')) AND P.NAME='` + child + `')` + inherited + `)
 WHERE SOURCE_PRIORITY=1` + selection
	if options.EffectiveADB {
		query = projection + `SELECT P.VALUE, P.NAME AS PARAMETER_NAME, P.CON_ID AS SOURCE_CONTAINER_ID, COUNT(*) OVER () AS SOURCE_ROWS
 FROM V$PARAMETER P WHERE P.CON_ID=TO_NUMBER(SYS_CONTEXT('USERENV','CON_ID')) AND P.NAME='` + child + `'
 AND P.ISSES_MODIFIABLE='FALSE' AND SYS_CONTEXT('USERENV','CLOUD_SERVICE') IN ('DWCS','OLTP','JDCS'))`
		if parameter == SGA {
			query += " WHERE VALUE IS NULL OR TO_NUMBER(VALUE)>0"
		}
	}
	return query, nil
}

func numeric(expression string) string {
	return "TO_CHAR(" + expression + ", 'TM9', 'NLS_NUMERIC_CHARACTERS=''.,''')"
}
