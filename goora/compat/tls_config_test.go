package compat

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql/driver"
	"errors"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectorUsesCustomTLSConfig(t *testing.T) {
	certificate, roots := connectorTLSCertificate(t)
	for _, mode := range []Mode{V2, V3} {
		t.Run(mode.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()

			dsn := "oracle://fixture:synthetic@" + listener.Addr().String() + "/fixture?SSL=enable"
			var verified atomic.Int32
			config := &tls.Config{
				RootCAs: roots, MinVersion: tls.VersionTLS12, ServerName: "caller.invalid",
				VerifyConnection: func(state tls.ConnectionState) error {
					if len(state.VerifiedChains) == 0 {
						return errors.New("TLS certificate verification did not run")
					}
					verified.Add(1)
					return nil
				},
			}
			connector, err := NewConnectorWithTLSConfig(dsn, Options{Mode: mode}, config)
			if err != nil {
				t.Fatal(err)
			}
			// The original object can be reassigned after construction. The
			// shared driver factory must carry the original snapshot forward.
			config.RootCAs = x509.NewCertPool()
			reopened, err := connector.Driver().(driver.DriverContext).OpenConnector(dsn)
			if err != nil {
				t.Fatal(err)
			}

			peer := make(chan error, 3)
			serverConfig := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
			go func() {
				for range cap(peer) {
					raw, acceptErr := listener.Accept()
					if acceptErr != nil {
						peer <- acceptErr
						continue
					}
					go func() {
						conn := tls.Server(raw, serverConfig)
						defer conn.Close()
						// There is no Oracle server. Completing TLS is the proof;
						// end the transport before Oracle admission.
						peer <- conn.HandshakeContext(ctx)
					}()
				}
			}()

			failures := make(chan error, 3)
			for _, candidate := range []driver.Connector{connector, connector, reopened} {
				go func() {
					conn, connectErr := candidate.Connect(ctx)
					if conn != nil {
						_ = conn.Close()
					}
					failures <- connectErr
				}()
			}
			for range cap(peer) {
				select {
				case err := <-peer:
					if err != nil {
						t.Errorf("custom trust TLS handshake failed: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("TLS handshake did not finish")
				}
			}
			for range cap(failures) {
				select {
				case err := <-failures:
					if err == nil {
						t.Error("TLS-only peer completed Oracle admission")
					}
				case <-ctx.Done():
					t.Fatal("Oracle connector did not return after TLS peer closed")
				}
			}
			if verified.Load() != int32(cap(peer)) {
				t.Fatalf("verified %d TLS handshakes, want %d", verified.Load(), cap(peer))
			}
			if config.ServerName != "caller.invalid" {
				t.Fatal("native driver mutated the caller's TLS configuration")
			}
		})
	}
}

func connectorTLSCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots
}
