package compat

import (
	"errors"
	"strings"
	"testing"
)

func TestNewNumberRejectsMalformedAndInexactInput(t *testing.T) {
	for _, input := range []string{"", "-", ".", "abc", "1..0", "1e", "1e++2", "NaN", "Infinity", " 1", "1 ", "1_0", "1e126", "1e-131", strings.Repeat("1", 39), "1e99999999999999999999", strings.Repeat("0", 1025)} {
		t.Run(input, func(t *testing.T) {
			number, err := NewNumber(input)
			if number != nil || !errors.Is(err, ErrInvalidNumber) {
				t.Fatalf("got %v, %v; want nil, ErrInvalidNumber", number, err)
			}
		})
	}
}

func TestNewNumberPreservesDecimalBoundaries(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"9007199254740993", "9007199254740993"}, {"+001.2300E+2", "123"}, {".00120", "0.0012"}, {"-0", "0"},
		{"99999999999999999999999999999999999999", "99999999999999999999999999999999999999"},
		{"-12345678901234567890123456789012345678", "-12345678901234567890123456789012345678"},
		{"9.99e125", "999" + strings.Repeat("0", 123)}, {"1e-130", "0." + strings.Repeat("0", 129) + "1"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			number, err := NewNumber(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			got, err := number.String()
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
