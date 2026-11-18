package goora_test

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"testing"

	"github.com/marcuoli/go-oracle-session/goora"
)

func TestPrepareURLPreservesOtherFieldsAndDisablesFastLogin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, query, want string }{
		{"empty", "", "FAST+LOGIN=FALSE"},
		{"enabled", "FAST+LOGIN=TRUE", "FAST+LOGIN=FALSE"},
		{"disabled", "FAST+LOGIN=FALSE", "FAST+LOGIN=FALSE"},
		{"raw space key", "FAST LOGIN=ENABLE", "FAST+LOGIN=FALSE"},
		{"case and encoded duplicates", "fast+login=TRUE&Fast%20Login=ENABLE&%46AST+LOGIN=false", "FAST+LOGIN=FALSE"},
		{"upstream Unicode uppercase", "FAST+LOG%C4%B1N=TRUE", "FAST+LOGIN=FALSE"},
		{"other options", "SSL=enable&wallet=rel%2fdir&wallet=second&wallet+password=p%2bq&prefetch_rows=500&lob+fetch=pre&x=a+b", "FAST+LOGIN=FALSE&SSL=enable&lob+fetch=pre&prefetch_rows=500&wallet=rel%2Fdir&wallet=second&wallet+password=p%2Bq&x=a+b"},
		{"between fields", "a=%26&fast+login=TRUE&b=%2f", "FAST+LOGIN=FALSE&a=%26&b=%2F"},
		{"unrelated names and values", "FAST_LOGIN=TRUE&x=FAST+LOGIN%3DTRUE&FAST+LOGINNING=TRUE", "FAST+LOGIN=FALSE&FAST+LOGINNING=TRUE&FAST_LOGIN=TRUE&x=FAST+LOGIN%3DTRUE"},
		{"empty query fields", "a=1&&Fast+Login=TRUE&b=2&", "FAST+LOGIN=FALSE&a=1&b=2"},
		{"escaped semicolon", "x=a%3bb", "FAST+LOGIN=FALSE&x=a%3Bb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := &url.URL{
				Scheme: "oracle", User: url.UserPassword(" user+@:/?#雪 ", " p+%;&/?=#雪 "),
				Host: "[2001:db8::1]:1521", Path: "/service/name", RawPath: "/service%2fname",
				RawQuery: tc.query, ForceQuery: true, Fragment: "fragment ?", RawFragment: "fragment%20%3f",
			}
			before := *u
			want := before
			want.RawQuery = tc.want
			if err := goora.PrepareURL(u); err != nil || !reflect.DeepEqual(*u, want) {
				t.Fatalf("PrepareURL changed unexpected fields or query: got %q, %v", u.RawQuery, err)
			}
			if err := goora.PrepareURL(u); err != nil || !reflect.DeepEqual(*u, want) {
				t.Fatalf("PrepareURL is not idempotent: query=%q, error=%v", u.RawQuery, err)
			}
		})
	}
}

func TestPrepareURLRejectsInvalidQueryAtomically(t *testing.T) {
	t.Parallel()
	if err := goora.PrepareURL(nil); !errors.Is(err, goora.ErrInvalidURL) {
		t.Fatalf("PrepareURL(nil) = %v, want ErrInvalidURL", err)
	}
	for _, query := range []string{
		"FAST+LOGIN=TRUE&private=%zz",
		"bad%zz=private&FAST+LOGIN=TRUE",
		"FAST+LOGIN=TRUE&private=a;b",
	} {
		t.Run(query, func(t *testing.T) {
			u := &url.URL{Scheme: "oracle", Host: "db.example.invalid", RawQuery: query}
			before := *u
			if err := goora.PrepareURL(u); err != goora.ErrInvalidURL || !reflect.DeepEqual(*u, before) {
				t.Fatalf("invalid query did not retain static error/unchanged URL: %v", err)
			}
		})
	}
}

func ExamplePrepareURL() {
	u := &url.URL{Scheme: "oracle", Host: "db.example.invalid:1521", Path: "/service",
		RawQuery: "SSL=enable&fast+login=TRUE"}
	if err := goora.PrepareURL(u); err != nil {
		fmt.Println("invalid connection query")
		return
	}
	fmt.Println(u.RawQuery)
	// Output: FAST+LOGIN=FALSE&SSL=enable
}
