package compat

import (
	"database/sql"
	"database/sql/driver"
	"fmt"

	legacy "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2"
	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
	modernTypes "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/types"
)

// Out describes an Oracle output or input/output bind. Size is character
// capacity for text. Dest remains caller owned and must not be accessed during
// execution. Discard output values after any execution error.
type Out struct {
	Dest any
	Size int
	In   bool
}

func adaptNamedValue(value *driver.NamedValue, mode Mode, owners ...*connectionHealth) error {
	switch input := value.Value.(type) {
	case Out:
		return adaptOutput(value, input, mode, owners...)
	case *Out:
		if input == nil {
			return fmt.Errorf("nil Oracle output descriptor")
		}
		return adaptOutput(value, *input, mode, owners...)
	case sql.Out:
		return adaptOutput(value, Out{Dest: input.Dest, In: input.In}, mode, owners...)
	case *Clob:
		destination, err := input.native(mode, owners...)
		if err != nil {
			return err
		}
		value.Value = destination
	case *legacy.Number:
		if mode == V3 {
			if input == nil {
				value.Value = nil
				return nil
			}
			text, err := input.String()
			if err != nil {
				return err
			}
			number, err := modernTypes.NewNumber(text)
			if err != nil {
				return err
			}
			value.Value = number
		}
	}
	return nil
}
func adaptOutput(value *driver.NamedValue, output Out, mode Mode, owners ...*connectionHealth) error {
	destination := output.Dest
	if clob, ok := destination.(*Clob); ok {
		var err error
		destination, err = clob.native(mode, owners...)
		if err != nil {
			return err
		}
	}
	if mode == V2 {
		value.Value = legacy.Out{Dest: destination, Size: output.Size, In: output.In}
	} else {
		value.Value = modern.Out{Dest: modernOutputDestination(destination), Size: output.Size, In: output.In}
	}
	return nil
}
