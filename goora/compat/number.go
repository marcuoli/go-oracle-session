package compat

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	legacy "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2"
)

// Number retains an exact Oracle NUMBER representation and its String method.
// Compatibility adapters convert it between backends through exact decimal
// text, never through floating point.
type Number = legacy.Number

// ErrInvalidNumber means decimal input is malformed or cannot be represented
// exactly within NewNumber's documented limits. It contains no input text.
var ErrInvalidNumber = errors.New("invalid exact Oracle NUMBER")

var decimalNumber = regexp.MustCompile(`^([+-]?)([0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE]([+-]?[0-9]+))?$`)

// NewNumber parses signed decimal text, optionally with a decimal point and
// base-ten exponent, without rounding. Input is bounded to 1024 bytes and 38
// significant digits; nonzero magnitude must be at least 1e-130 and below 1e126.
// Whitespace, non-decimal values, overflow and underflow return ErrInvalidNumber.
func NewNumber(text string) (*Number, error) {
	if len(text) > 1024 {
		return nil, ErrInvalidNumber
	}
	match := decimalNumber.FindStringSubmatch(text)
	if match == nil {
		return nil, ErrInvalidNumber
	}
	exponent := 0
	if match[3] != "" {
		value, err := strconv.ParseInt(match[3], 10, 32)
		if err != nil || value < -2048 || value > 2048 {
			return nil, ErrInvalidNumber
		}
		exponent = int(value)
	}
	mantissa := match[2]
	point := strings.IndexByte(mantissa, '.')
	if point < 0 {
		point = len(mantissa)
	}
	digits := strings.ReplaceAll(mantissa, ".", "")
	leading := len(digits) - len(strings.TrimLeft(digits, "0"))
	digits = strings.TrimRight(digits[leading:], "0")
	if digits == "" {
		return legacy.NewNumber("0")
	}
	exponent += point - leading - 1
	if len(digits) > 38 || exponent < -130 || exponent > 125 {
		return nil, ErrInvalidNumber
	}
	// The retained encoder expects a normalized first digit. Normalize before
	// delegation so leading zeros, a plus sign or an uppercase exponent cannot
	// silently change the encoded value or enter its unchecked empty-input path.
	normalized := digits[:1]
	if len(digits) > 1 {
		normalized += "." + digits[1:]
	}
	if match[1] == "-" {
		normalized = "-" + normalized
	}
	normalized += "e" + strconv.Itoa(exponent)
	return legacy.NewNumber(normalized)
}
