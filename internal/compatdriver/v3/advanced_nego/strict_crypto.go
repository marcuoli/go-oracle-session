package advanced_nego

import (
	"crypto/aes"
	"errors"
)

// ErrStrictCryptoUnavailable is a terminal error for an alternate that cannot
// negotiate the upgraded native encryption or integrity service.
var ErrStrictCryptoUnavailable = errors.New("upgraded Oracle native crypto unavailable")

// RequireModernCrypto enables the strict Oracle Net exchange for this attempt.
// Call before Write. The normal retained V3 exchange is unchanged.
func (nego *AdvNego) RequireModernCrypto() {
	nego.strictCrypto = true
}

func upgradedService(version uint32) bool {
	// Oracle Net service version word, not the SQL server version.
	return version>>24 >= 23 || (version>>12)&0xff == 1
}

func (nego *AdvNego) prepareModernCrypto(authService string) error {
	encryption := nego.serviceList[2].(*encryptService)
	integrity := nego.serviceList[3].(*dataIntegrityService)
	if authService != "" || !encryption.algorithmReceived || !integrity.algorithmReceived {
		return ErrStrictCryptoUnavailable
	}
	// Honor the original service offers, including REQUIRED and REJECTED.
	// Only AES and SHA-2 are supported by this upgraded exchange.
	if (encryption.algoID == 0 && integrity.algoID == 0) ||
		!encryption.acceptsUpgradedChoice(encryption.algoID, 15, 17) ||
		!integrity.acceptsUpgradedChoice(integrity.algoID, 4, 6) ||
		len(integrity.sharedKey) < 4*aes.BlockSize {
		return ErrStrictCryptoUnavailable
	}

	// Upstream 62f8a42 derives the upgraded IV from the DH shared secret's
	// second 32 bytes. The server's variable-length legacy IV is never an AES IV
	// on this route.
	integrity.modernIV = make([]byte, 2*aes.BlockSize)
	copy(integrity.modernIV, integrity.sharedKey[2*aes.BlockSize:])
	if encryption.algoID != 0 {
		encryption.modernIV = integrity.modernIV[:aes.BlockSize]
	}
	return nil
}

func (serv *defaultService) acceptsUpgradedChoice(id, minID, maxID int) bool {
	if id != 0 && (id < minID || id > maxID || !upgradedService(serv.version)) {
		return false
	}
	for _, index := range serv.selectedIndices {
		if serv.availableServiceIDs[index] == id {
			return true
		}
	}
	return false
}

// FoldKey rekeys the negotiated services after successful password
// authentication and before the first authenticated Oracle Net packet.
func (nego *AdvNego) FoldKey(key []byte) error {
	integrity := nego.serviceList[3].(*dataIntegrityService)
	if len(key) == 0 || len(integrity.modernIV) == 0 {
		return ErrStrictCryptoUnavailable
	}
	for i := 0; i < min(len(key), len(integrity.sharedKey)); i++ {
		integrity.sharedKey[i] ^= key[i]
	}
	for i := 0; i < min(len(key), len(integrity.modernIV)); i++ {
		integrity.modernIV[i] ^= key[i]
	}
	if err := nego.StartServices(); err != nil {
		return err
	}
	nego.comm.session.Context.AdvancedService.KeyFolded = true
	return nil
}
