package compat_test

import (
	"errors"
	"io/fs"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/goora/compat"
)

func TestInspectV2ConfigDefaults(t *testing.T) {
	got, err := compat.InspectV2Config("oracle://fixture:private@primary.example:1521/service")
	if err != nil {
		t.Fatal(err)
	}
	want := compat.V2ConfigSnapshot{
		ConnectTimeout: time.Minute,
		Endpoints:      []compat.V2Endpoint{{Protocol: "tcp", Host: "primary.example", Port: 1521}},
		ServiceName:    "service",
		AuthMode:       "normal",
		TLSVerify:      true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configuration = %+v, want %+v", got, want)
	}
}

func TestInspectV2ConfigDescriptor(t *testing.T) {
	query := url.Values{
		"connStr": {"(DESCRIPTION=(ADDRESS_LIST=" +
			"(ADDRESS=(PROTOCOL=TcPs)(HOST=first.example)(PORT=1522))" +
			"(ADDRESS=(PROTOCOL=TCP)(HOST=second.example)(PORT=1523)))" +
			"(CONNECT_DATA=(SERVICE_NAME=service)(SID=fixture_sid)(INSTANCE_NAME=instance)))"},
		"CONNECTION TIMEOUT": {"7"},
		"DBA PRIVILEGE":      {"SYSOPER"},
		"SSL":                {"ENABLE"},
		"SSL VERIFY":         {"FALSE"},
	}
	got, err := compat.InspectV2Config("oracle://fixture:private@:0/?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	want := compat.V2ConfigSnapshot{
		ConnectTimeout: 7 * time.Second,
		Endpoints: []compat.V2Endpoint{
			{Protocol: "tcps", Host: "first.example", Port: 1522},
			{Protocol: "tcp", Host: "second.example", Port: 1523},
		},
		ServiceName:  "service",
		SID:          "fixture_sid",
		InstanceName: "instance",
		AuthMode:     "SYSOPER",
		TLS:          true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configuration = %+v, want %+v", got, want)
	}
}

func TestInspectV2ConfigKeepsParserErrors(t *testing.T) {
	got, err := compat.InspectV2Config("oracle://fixture:private@primary.example:1521")
	if err == nil || err.Error() != "empty SID and service name" || !reflect.DeepEqual(got, compat.V2ConfigSnapshot{}) {
		t.Fatalf("missing service returned %+v, %v", got, err)
	}
	query := url.Values{"WALLET": {t.TempDir()}}
	got, err = compat.InspectV2Config("oracle://fixture:private@primary.example:1521/service?" + query.Encode())
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || !errors.Is(err, fs.ErrNotExist) ||
		!strings.HasSuffix(pathErr.Path, "cwallet.sso") || !reflect.DeepEqual(got, compat.V2ConfigSnapshot{}) {
		t.Fatalf("wallet error returned %+v, %v", got, err)
	}
}
