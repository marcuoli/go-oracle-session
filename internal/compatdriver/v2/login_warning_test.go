package go_ora

import (
	"fmt"
	"io"
	"os"
	"sync"
	"testing"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/network"
)

func TestLoginWarningsStayWithTheirConnection(t *testing.T) {
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			want := fmt.Sprintf("warning for connection %d", i)
			var got string
			connector := NewConnector("").(*OracleConnector)
			connector.WithLoginWarningHandler(func(raw string) bool { got = raw; return true })
			conn := &Connection{State: Closed, loginWarningHandler: connector.loginWarningHandler}
			conn.reportWarning(want)
			if got != want {
				t.Errorf("warning = %q, want %q", got, want)
			}
		}(i)
	}
	workers.Wait()
}

func TestLoginWarningWireMessageReachesHandler(t *testing.T) {
	const message = "ORA-28002: password expires soon\n"
	input := append([]byte{2, 0x6d, 0x62, 1, byte(len(message)), 1, 4, byte(len(message))}, []byte(message)...)
	var got string
	conn := &Connection{State: Closed, session: network.NewSessionWithInputBufferForDebug(input), loginWarningHandler: func(raw string) bool { got = raw; return true }}
	if err := conn.readMsg(15); err != nil {
		t.Fatal(err)
	}
	if want := "&{28002 4 " + message + "}\n"; got != want {
		t.Fatalf("warning = %q, want %q", got, want)
	}
}

func TestLoginWarningFallbackPreservesDriverOutput(t *testing.T) {
	for _, state := range []ConnectionState{Closed, Opened} {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		original := os.Stderr
		func() {
			os.Stderr = writer
			defer func() { os.Stderr = original; writer.Close() }()
			conn := &Connection{State: state, loginWarningHandler: func(string) bool { return false }}
			conn.reportWarning("unchanged warning\n")
		}()
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || string(data) != "unchanged warning\n" {
			t.Fatalf("fallback = %q, %v", data, err)
		}
	}
}
