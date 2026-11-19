package compat

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	legacy "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2"
	legacyNetwork "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/network"
	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
	modernConfig "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/configurations"
	modernTypes "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/types"
)

func TestOutputRetainsCapacityAndDestination(t *testing.T) {
	for _, mode := range []Mode{V2, V3} {
		var destination int64
		value := driver.NamedValue{Name: "result", Value: Out{Dest: &destination, Size: 32767, In: true}}
		if err := adaptNamedValue(&value, mode); err != nil {
			t.Fatal(err)
		}
		switch mode {
		case V2:
			out, ok := value.Value.(legacy.Out)
			if !ok || out.Dest != &destination || out.Size != 32767 || !out.In {
				t.Fatalf("legacy descriptor=%#v", value.Value)
			}
		case V3:
			out, ok := value.Value.(modern.Out)
			if !ok || out.Dest != &destination || out.Size != 32767 || !out.In {
				t.Fatalf("modern descriptor=%#v", value.Value)
			}
		}
	}
}

func TestClobOutputLifecycleAndExactValues(t *testing.T) {
	for _, mode := range []Mode{V2, V3} {
		for _, content := range []any{nil, "", "é漢  exact  "} {
			var clob Clob
			bind := driver.NamedValue{Value: Out{Dest: &clob}}
			if err := adaptNamedValue(&bind, mode); err != nil {
				t.Fatal(err)
			}
			var err error
			if mode == V2 {
				destination := bind.Value.(legacy.Out).Dest.(*legacy.Clob)
				if content == nil {
					destination.Valid = false
				} else {
					err = destination.Scan(content)
				}
			} else {
				err = bind.Value.(modern.Out).Dest.(*modernTypes.Clob).SetValue(content)
				if content == "" {
					bind.Value.(modern.Out).Dest.(*modernTypes.Clob).SetBytes([]byte{})
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := clob.Read(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, err := clob.Value()
			if err != nil || got != content {
				t.Fatalf("mode=%s got=%#v err=%v want=%#v", mode, got, err, content)
			}
			if err := clob.SetValue("replacement"); !errors.Is(err, ErrLOBInUse) {
				t.Fatalf("live locator overwritten: %v", err)
			}
			if err := clob.Free(); err != nil {
				t.Fatal(err)
			}
			if err := clob.Free(); err != nil {
				t.Fatal(err)
			}
			if _, err := clob.Value(); !errors.Is(err, ErrLOBClosed) {
				t.Fatalf("closed value=%v", err)
			}
			if err := clob.SetValue("reused"); err != nil {
				t.Fatal(err)
			}
			got, err = clob.Value()
			if err != nil || got != "reused" {
				t.Fatalf("reset value=%v %v", got, err)
			}
		}
	}
}

func TestStandardOutputAndExactNumber(t *testing.T) {
	n, err := NewNumber("9007199254740993")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []Mode{V2, V3} {
		v := driver.NamedValue{Value: n}
		if err := adaptNamedValue(&v, mode); err != nil {
			t.Fatal(err)
		}
		text, err := v.Value.(interface{ String() (string, error) }).String()
		if err != nil || text != "9007199254740993" {
			t.Fatalf("rounded NUMBER=%s %v", text, err)
		}
		var output Clob
		v.Value = sql.Out{Dest: &output}
		if err := adaptNamedValue(&v, mode); err != nil {
			t.Fatal(err)
		}
	}
	code, ok := OracleCode(errors.Join(context.DeadlineExceeded, &legacyNetwork.OracleError{ErrCode: 1013}))
	if !ok || code != 1013 {
		t.Fatalf("typed code=%d %v", code, ok)
	}
}

type lobStreamFixture struct {
	modernTypes.LobStreamer
	data               []byte
	readErr, freeErr   error
	ctx                context.Context
	reads, ends, frees int
}

func (s *lobStreamFixture) GetLocator() modernTypes.Locator { return make(modernTypes.Locator, 40) }
func (s *lobStreamFixture) GetLobReadMode() modernConfig.LobReadMode {
	return modernConfig.LobReadMode_NONE
}
func (s *lobStreamFixture) StartContext(ctx context.Context) chan struct{} {
	s.ctx = ctx
	return make(chan struct{})
}
func (s *lobStreamFixture) EndContext(chan struct{})          { s.ends++ }
func (s *lobStreamFixture) Read(int64, int64) ([]byte, error) { s.reads++; return s.data, s.readErr }
func (s *lobStreamFixture) FreeTemporaryLocator() error       { s.frees++; return s.freeErr }

func TestClobSelectedInputPreservesEmptyAndNull(t *testing.T) {
	for _, mode := range []Mode{V2, V3} {
		for _, input := range []any{nil, "", []byte("é漢")} {
			var c Clob
			if err := c.SetValue(input); err != nil {
				t.Fatal(err)
			}
			if _, err := c.native(mode); err != nil {
				t.Fatal(err)
			}
			got, err := c.Value()
			want := input
			if bytes, ok := input.([]byte); ok {
				want = string(bytes)
			}
			if err != nil || got != want {
				t.Fatalf("mode=%s input=%#v value=%#v %v", mode, input, got, err)
			}
		}
	}
}

func TestLazyClobContextExactnessAndDisposal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		data  []byte
		cause error
		want  any
	}{
		{"unicode", []byte{0, 0xe9, 0x6f, 0x22}, nil, "é漢"},
		{"non-null empty", nil, nil, ""},
		{"failed read", []byte{0, 0xe9}, io.ErrUnexpectedEOF, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := &modernTypes.Clob{}
			if err := native.SetValue("seed"); err != nil {
				t.Fatal(err)
			}
			native.SetBytes(nil)
			stream := &lobStreamFixture{data: tc.data, readErr: tc.cause}
			native.SetStreamer(stream)
			var c Clob
			if err := c.Scan(native); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Value(); !errors.Is(err, ErrLOBUnread) {
				t.Fatalf("unread value exposed: %v", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			err := c.Read(ctx)
			if !errors.Is(err, tc.cause) || stream.ctx != ctx || stream.reads != 1 || stream.ends != 1 {
				t.Fatalf("read/join=%v %d %d", err, stream.reads, stream.ends)
			}
			value, err := c.Value()
			if !errors.Is(err, tc.cause) || value != tc.want {
				t.Fatalf("read result=%#v %v", value, err)
			}
			if tc.cause != nil {
				_ = c.Read(ctx)
				if stream.reads != 1 {
					t.Fatal("failed LOB read replayed")
				}
			}
			if err := c.Free(); err != nil {
				t.Fatal(err)
			}
			if err := c.Free(); err != nil {
				t.Fatal(err)
			}
			if stream.frees != 1 {
				t.Fatalf("locator frees=%d", stream.frees)
			}
		})
	}
}
