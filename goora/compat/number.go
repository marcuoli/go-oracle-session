package compat

import legacy "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2"

// Number retains an exact Oracle NUMBER representation and its String/Scan
// methods. Compatibility adapters convert it between backends through exact
// decimal text, never through floating point.
type Number = legacy.Number

// NewNumber parses exact decimal text for either compatibility backend.
func NewNumber(text string) (*Number, error) { return legacy.NewNumber(text) }
