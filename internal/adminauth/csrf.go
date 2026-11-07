package adminauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func validDigest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == sha256.Size && s == hex.EncodeToString(b)
}
func ValidateCSRF(p Principal, token string) error {
	if len(token) != 43 || !validDigest(p.CSRFHash) {
		return ErrForbidden
	}
	if subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(p.CSRFHash)) != 1 {
		return ErrForbidden
	}
	return nil
}
