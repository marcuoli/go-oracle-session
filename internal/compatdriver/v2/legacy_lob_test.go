package go_ora

import (
	"bytes"
	"testing"
)

func TestLegacyVariableWidthClobDecodesPackedCodeUnits(t *testing.T) {
	for _, little := range []bool{false, true} {
		packed := []byte{1, 0xe9, 2, 0x6f, 0x22, 0, 2, 0xd8, 0x3d, 2, 0xde, 0}
		locator := make([]byte, 8)
		locator[6] = 0x80
		if little {
			locator[7] = 0x40
		}
		lob := &Lob{connection: &Connection{dBVersion: &DBVersion{MajorVersion: 9}}, sourceLocator: locator, data: *bytes.NewBuffer(packed)}
		got, err := lob.decodedReadData()
		want := []byte{0, 0xe9, 0x6f, 0x22, 0, 0, 0xd8, 0x3d, 0xde, 0}
		if little {
			want = []byte{0xe9, 0, 0x22, 0x6f, 0, 0, 0x3d, 0xd8, 0, 0xde}
		}
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("little=%v got=%x err=%v want=%x", little, got, err, want)
		}
	}
}
func TestLegacyClobMalformedHasNoPrefixAndRetires(t *testing.T) {
	for _, data := range [][]byte{{3}, {2, 0}, {1, 65, 2}, {0x81, 1}} {
		locator := make([]byte, 8)
		locator[6] = 0x80
		conn := &Connection{dBVersion: &DBVersion{MajorVersion: 9}}
		lob := &Lob{connection: conn, sourceLocator: locator, data: *bytes.NewBuffer(data)}
		got, err := lob.decodedReadData()
		if got != nil || err == nil || !conn.bad {
			t.Fatalf("data=%x got=%x err=%v bad=%v", data, got, err, conn.bad)
		}
	}
}
func TestOtherLobWireDataRemainsLiteral(t *testing.T) {
	raw := []byte{0, 0xe9, 0x6f, 0x22}
	for _, tc := range []struct {
		major    int
		variable bool
	}{{9, false}, {10, true}, {12, true}, {23, true}, {26, true}, {99, true}} {
		locator := make([]byte, 8)
		if tc.variable {
			locator[6] = 0x80
		}
		lob := &Lob{connection: &Connection{dBVersion: &DBVersion{MajorVersion: tc.major}}, sourceLocator: locator, data: *bytes.NewBuffer(raw)}
		got, err := lob.decodedReadData()
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("major=%d variable=%v got=%x err=%v", tc.major, tc.variable, got, err)
		}
	}
}
