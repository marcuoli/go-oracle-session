package go_ora

import "testing"

func TestDiscardIsTerminalWithoutProtocolWork(t *testing.T) {
	connection := &Connection{State: Opened}
	if err := connection.Discard(); err != nil {
		t.Fatal(err)
	}
	if connection.State != Closed || !connection.bad {
		t.Fatal("discarded connection remains valid")
	}
	if err := connection.Discard(); err != nil {
		t.Fatal(err)
	}
}
