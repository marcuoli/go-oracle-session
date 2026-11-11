package memory_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/marcuoli/go-oracle-session/memory"
	"github.com/marcuoli/go-oracle-session/session"
)

func sample(value, source, parameter string) session.Result {
	columns := []string{"VALUE", "SOURCE_CONTAINER_ID", "CURRENT_CONTAINER_ID", "PARAMETER_NAME", "SOURCE_ROWS", "INSTANCE_ID"}
	values := []string{value, source, "3", parameter, "1", "2"}
	row := make([]session.Value, len(values))
	for i, text := range values {
		row[i].Text = text
	}
	return session.Result{Columns: columns, Rows: [][]session.Value{row}}
}

func TestDecodePreservesExactValuesAndScopedOrigins(t *testing.T) {
	for _, tc := range []struct {
		name, value, source, parameter string
		kind                           memory.Parameter
		origin                         memory.Origin
		options                        memory.PDBOptions
	}{
		{"local SGA", "2.125", "3", "sga_target", memory.SGA, memory.ContainerParameter, memory.PDBOptions{}},
		{"shared SGA", "8", "0", "sga_max_size", memory.SGA, memory.InheritedInstance, memory.PDBOptions{AllowInherited: true}},
		{"root PGA", "4", "1", "pga_aggregate_target", memory.PGA, memory.InheritedInstance, memory.PDBOptions{AllowInherited: true}},
		{"zero PGA", "0", "3", "pga_aggregate_target", memory.PGA, memory.ContainerParameter, memory.PDBOptions{}},
		{"exact large PGA", "9007199254740993.125", "3", "pga_aggregate_target", memory.PGA, memory.ContainerParameter, memory.PDBOptions{}},
		{"effective SGA", "7.813", "3", "sga_target", memory.SGA, memory.EffectiveADB, memory.PDBOptions{EffectiveADB: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := sample(tc.value, tc.source, tc.parameter)
			value, err := memory.DecodePDB(input, tc.kind, tc.options)
			if err != nil || value.State != memory.Available || value.ValueGiB != tc.value || value.Parameter != tc.parameter || value.Origin != tc.origin || value.CurrentContainerID != 3 || value.InstanceID != 2 {
				t.Fatal(value, err)
			}
			input.Rows[0][0].Text = "mutated"
			if value.ValueGiB != tc.value {
				t.Fatal("observation borrowed mutable input")
			}
		})
	}
}

func TestDecodeDistinguishesNullAndNoRows(t *testing.T) {
	null := sample("", "3", "sga_target")
	null.Rows[0][0].Null = true
	value, err := memory.DecodePDB(null, memory.SGA, memory.PDBOptions{})
	if err != nil || value.State != memory.Null || value.CurrentContainerID != 3 || value.InstanceID != 2 || value.ValueGiB != "" {
		t.Fatal(value, err)
	}
	null.Rows = nil
	value, err = memory.DecodePDB(null, memory.SGA, memory.PDBOptions{})
	if err != nil || value.State != memory.NoRows || value.SourceContainerID != 0 || value.CurrentContainerID != 0 || value.InstanceID != 0 {
		t.Fatal(value, err)
	}
}

func TestDecodeRejectsMalformedOrUnadmittedRows(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*session.Result)
	}{
		{"other container", func(r *session.Result) { r.Rows[0][1].Text = "4" }},
		{"inheritance not admitted", func(r *session.Result) { r.Rows[0][1].Text = "0"; r.Rows[0][3].Text = "sga_max_size" }},
		{"root connection", func(r *session.Result) { r.Rows[0][2].Text = "1" }},
		{"missing instance", func(r *session.Result) { r.Rows[0][5].Null = true }},
		{"duplicate rows", func(r *session.Result) { r.Rows = append(r.Rows, r.Rows[0]) }},
		{"duplicate behind filtered zero", func(r *session.Result) { r.Rows[0][4].Text = "2" }},
		{"duplicate columns", func(r *session.Result) { r.Columns[1] = "VALUE" }},
		{"malformed missing row schema", func(r *session.Result) { r.Rows = nil; r.Columns = []string{"VALUE"} }},
		{"missing value cell", func(r *session.Result) { r.Rows[0] = r.Rows[0][1:] }},
		{"wrong parameter", func(r *session.Result) { r.Rows[0][3].Text = "sga_max_size" }},
		{"zero PDB SGA", func(r *session.Result) { r.Rows[0][0].Text = "0" }},
		{"negative", func(r *session.Result) { r.Rows[0][0].Text = "-1" }},
		{"fraction", func(r *session.Result) { r.Rows[0][0].Text = "1/2" }},
		{"exponent", func(r *session.Result) { r.Rows[0][0].Text = "1e999999999" }},
		{"multiple decimal points", func(r *session.Result) { r.Rows[0][0].Text = "1.2.3" }},
		{"oversized", func(r *session.Result) { r.Rows[0][0].Text = strings.Repeat("1", 129) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := sample("2", "3", "sga_target")
			tc.change(&input)
			if _, err := memory.DecodePDB(input, memory.SGA, memory.PDBOptions{}); !errors.Is(err, memory.ErrInvalidEvidence) {
				t.Fatal("invalid evidence accepted", err)
			}
		})
	}
}
