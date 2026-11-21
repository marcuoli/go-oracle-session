package go_ora

import (
	"crypto/cipher"
	"crypto/des"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"strings"
)

// BreakCurrentOperation lets the connection owner request Oracle's ordinary
// break without starting a second context watcher or exposing the wire session.
// The owner must join both the operation and this call before testing settlement
// or closing/reusing the connection.
func (conn *Connection) BreakCurrentOperation() error {
	if conn == nil || conn.session == nil {
		return driver.ErrBadConn
	}
	return conn.session.BreakConnection()
}

// CancellationSettled is inspected only after the operation and break workers
// have joined. It does not send SQL, reconnect, or change connection state.
func (conn *Connection) CancellationSettled() bool {
	return conn != nil && conn.State == Opened && !conn.bad && conn.session != nil && !conn.session.IsBreak()
}

// usesLegacyOperations selects the TTC operations available before 10.1.0.2.
// It never selects a different driver or retries authentication.
func (conn *Connection) usesLegacyOperations() bool {
	return conn.dBVersion != nil && conn.dBVersion.Number < 10102
}

// SupportsProtocolPing reports whether the existing Ping path can send the
// native command without using its legacy SELECT fallback.
func (conn *Connection) SupportsProtocolPing() bool {
	return conn != nil && conn.dBVersion != nil && !conn.usesLegacyOperations()
}

func (conn *Connection) validateSupportedServer() error {
	if conn.dBVersion != nil && conn.dBVersion.MajorVersion < 9 {
		return fmt.Errorf("Oracle versions earlier than 9 are not supported")
	}
	return nil
}

func decryptLegacySessionKey(key []byte, challenge string) ([]byte, error) {
	encoded, err := hex.DecodeString(challenge)
	if err != nil || len(encoded) != des.BlockSize {
		return nil, fmt.Errorf("invalid legacy session challenge: expected one DES block")
	}
	block, err := des.NewCipher(key)
	if err != nil {
		return nil, err
	}
	decoded := make([]byte, len(encoded))
	cipher.NewCBCDecrypter(block, make([]byte, des.BlockSize)).CryptBlocks(decoded, encoded)
	return decoded, nil
}

func encryptLegacyPassword(password string, key []byte) (string, error) {
	block, err := des.NewCipher(key)
	if err != nil {
		return "", err
	}
	padding := (des.BlockSize - len([]byte(password))%des.BlockSize) % des.BlockSize
	plain := append([]byte(password), make([]byte, padding)...)
	encoded := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, make([]byte, des.BlockSize)).CryptBlocks(encoded, plain)
	return strings.ToUpper(hex.EncodeToString(encoded)) + fmt.Sprint(padding), nil
}
