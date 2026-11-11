package memory

import (
	"errors"
	"strconv"
	"strings"

	"github.com/marcuoli/go-oracle-session/session"
)

// State distinguishes a measured setting from no row and an explicit SQL NULL.
type State string

// Available, NoRows and Null preserve absence without manufacturing numeric zero.
const (
	Available State = "available"
	NoRows    State = "no_rows"
	Null      State = "null"
)

// Origin identifies configured parameter scope, never a consumption measurement.
type Origin string

// ContainerParameter, InheritedInstance and EffectiveADB identify the chosen source.
const (
	ContainerParameter Origin = "pdb_parameter"
	InheritedInstance  Origin = "inherited_instance_parameter"
	EffectiveADB       Origin = "adb_container_parameter"
)

// Observation owns exact GiB text and the source/current container and instance
// identities. NULL retains valid provenance; NoRows has no inferred identities.
// An inherited setting is not a PDB-specific allocation or a cluster total.
type Observation struct {
	State              State
	ValueGiB           string
	Parameter          string
	Origin             Origin
	SourceContainerID  int
	CurrentContainerID int
	InstanceID         int
}

// ErrInvalidEvidence rejects malformed, duplicate, ambiguous or out-of-scope rows.
// Such errors must remain visible and must not trigger an effective-value retry.
var ErrInvalidEvidence = errors.New("invalid configured-memory evidence")

// DecodePDB validates a result from PDBStatement using the identical options.
// It copies scalar strings and never mutates the result. A missing/NULL value is
// distinct from invalid evidence. Numeric text is bounded and kept exact; binary
// floating-point, exponent expansion and fractions are not used. Caller session
// ownership must bind this result to the admitted endpoint and expected instance.
func DecodePDB(result session.Result, parameter Parameter, options PDBOptions) (Observation, error) {
	if parameter != SGA && parameter != PGA {
		return Observation{}, ErrUnsupportedParameter
	}
	if len(result.Columns) != 6 || len(result.Rows) > 1 {
		return Observation{}, ErrInvalidEvidence
	}
	columns := map[string]int{}
	for i, name := range result.Columns {
		name = strings.ToLower(name)
		if _, exists := columns[name]; name == "" || exists {
			return Observation{}, ErrInvalidEvidence
		}
		columns[name] = i
	}
	for _, row := range result.Rows {
		if len(row) != len(result.Columns) {
			return Observation{}, ErrInvalidEvidence
		}
	}

	for _, name := range []string{"value", "source_container_id", "current_container_id", "parameter_name", "source_rows", "instance_id"} {
		if _, ok := columns[name]; !ok {
			return Observation{}, ErrInvalidEvidence
		}
	}
	if len(result.Rows) == 0 {
		return Observation{State: NoRows}, nil
	}
	cell := func(name string) session.Value { return result.Rows[0][columns[name]] }
	integer := func(name string) (int, bool) {
		v := cell(name)
		n, err := strconv.Atoi(v.Text)
		return n, err == nil && !v.Null
	}
	current, currentOK := integer("current_container_id")
	source, sourceOK := integer("source_container_id")
	instance, instanceOK := integer("instance_id")
	if !currentOK || !sourceOK || !instanceOK || instance <= 0 || current <= 2 || cell("source_rows").Null || cell("source_rows").Text != "1" {
		return Observation{}, ErrInvalidEvidence
	}
	if source != current && (options.EffectiveADB || !options.AllowInherited || source != 0 && source != 1) {
		return Observation{}, ErrInvalidEvidence
	}
	expected := "pga_aggregate_target"
	if parameter == SGA {
		expected = "sga_target"
		if source != current {
			expected = "sga_max_size"
		}
	}
	if cell("parameter_name").Null || cell("parameter_name").Text != expected {
		return Observation{}, ErrInvalidEvidence
	}
	observation := Observation{State: Available, Parameter: expected, Origin: ContainerParameter, SourceContainerID: source, CurrentContainerID: current, InstanceID: instance}
	if source != current {
		observation.Origin = InheritedInstance
	}
	if options.EffectiveADB {
		observation.Origin = EffectiveADB
	}
	value := cell("value")
	if value.Null {
		observation.State = Null
		return observation, nil
	}
	digits := strings.Replace(value.Text, ".", "", 1)
	if len(value.Text) > 128 || digits == "" || strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) >= 0 || parameter == SGA && source == current && strings.Trim(digits, "0") == "" {
		return Observation{}, ErrInvalidEvidence
	}
	observation.ValueGiB = value.Text
	return observation, nil
}
