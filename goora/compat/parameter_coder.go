package compat

import (
	"database/sql"
	"errors"
	"math"
	"reflect"

	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/converters"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/network"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/parameter_coder"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/types"
)

func newModernDriverConnector(connector *modern.OracleConnector) *modernDriverConnector {
	// NewConnector owns an isolated OracleDriver. Register before exposing it:
	// connections share its coder maps, and those maps must then remain read-only.
	// AddParameterCoder requires sql.DB; this temporary handle never connects.
	db := sql.OpenDB(connector)
	var setupErr error
	for _, dataType := range []uint16{types.NCHAR, types.CHAR} {
		if err := modern.AddParameterCoder(db, types.TyString, dataType, &modernCharacterParameter{}); err != nil {
			setupErr = err
			break
		}
	}
	if setupErr == nil {
		setupErr = modern.AddParameterCoder(db, reflect.TypeFor[modernCharacterOutput](), types.NCHAR, &modernCharacterParameter{})
	}
	return &modernDriverConnector{OracleConnector: connector, setupErr: errors.Join(setupErr, db.Close())}
}

// modernCharacterOutput marks character OUT binds without changing the public
// size contract or the caller's destination. The coder API has no bind direction.
type modernCharacterOutput struct{ destination any }

func modernOutputDestination(destination any) any {
	switch value := destination.(type) {
	case *string:
		if value != nil {
			return &modernCharacterOutput{destination: value}
		}
	case *sql.NullString:
		if value != nil {
			return &modernCharacterOutput{destination: value}
		}
	}
	return destination
}

func (output *modernCharacterOutput) Scan(value any) error {
	return types.Copy(output.destination, value)
}

// modernCharacterParameter delegates string conversion to upstream, restoring
// character OUT capacity and the row-byte handling of untyped character NULLs.
type modernCharacterParameter struct {
	parameter_coder.StringParameter
}

func (parameter *modernCharacterParameter) Copy() parameter_coder.OracleParameterCoder {
	copy := *parameter
	return &copy
}

func (parameter *modernCharacterParameter) Encode(input any, connection parameter_coder.IConnection) error {
	output, isOutput := input.(modernCharacterOutput)
	declaredSize := max(parameter.MaxLen, parameter.MaxCharLen)
	if isOutput {
		input = output.destination
		// As in v2, choose the string datatype from the input value before
		// reserving output capacity. A 32767-character PL/SQL buffer alone
		// must not turn a VARCHAR2 bind into LONG on a 4000-byte SQL server.
		parameter.MaxLen, parameter.MaxCharLen = 0, 0
	}
	if err := parameter.StringParameter.Encode(input, connection); err != nil {
		return err
	}
	if !isOutput || parameter.DataType != types.NCHAR {
		return nil
	}
	// v2 advertised Size in characters and MaxLen in bytes. v3's public string
	// coder leaves them equal; its later non-input adjustment is unreachable.
	// Expand after upstream selects the datatype, preserving large OUT binds.
	converter, err := connection.GetStringCoder(parameter.CharsetID, parameter.CharsetForm)
	if err != nil {
		return err
	}
	width := int64(converters.MaxBytePerChar(converter.GetLangID()))
	parameter.MaxCharLen = max(parameter.MaxCharLen, declaredSize)
	if width <= 0 || parameter.MaxCharLen > math.MaxInt64/width {
		return errors.New("invalid Oracle character output capacity")
	}
	parameter.MaxLen = parameter.MaxCharLen * width
	return nil
}

func (parameter *modernCharacterParameter) Read(session network.SessionReader) error {
	// Some servers describe an untyped NULL with a byte length but no character
	// length. There is no CLR to consume; reading one steals the next column.
	// Declared strings (including NULL OUT binds), UDTs and arrays retain the
	// upstream reader and their normal CLR or fixed-CLR representation.
	if (parameter.DataType == types.NCHAR || parameter.DataType == types.CHAR) &&
		parameter.MaxCharLen == 0 && !parameter.IsUDTPar && !parameter.IsArrayPar {
		parameter.BValue = nil
		return nil
	}
	return parameter.StringParameter.Read(session)
}
