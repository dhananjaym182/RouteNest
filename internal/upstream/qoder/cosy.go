package qoder

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"cline-go-proxy/internal/deviceflow"
)

// buildCosyHeaders produces the full Cosy-* / Authorization header set for one
// Qoder request. body must be the exact bytes that will be sent (nil / empty
// for GET requests).
//
// The scheme:
//   - AES-128-CBC encrypt a small user-info JSON blob with a fresh key (the
//     first 16 characters of a UUID, hyphens included, so 16 ASCII bytes);
//   - wrap that AES key with RSA PKCS#1 v1.5 (base64 "cosyKey");
//   - sign payloadB64 + cosyKey + timestamp + raw body + sigPath with MD5.
//
// Header key casing matters on the wire (Cosy-Machineid, not Cosy-MachineID).
// http.Header canonicalizes keys, and every key below is already canonical.
func buildCosyHeaders(body []byte, requestURL string, creds Creds) (http.Header, error) {
	if creds.UserID == "" {
		return nil, fmt.Errorf("cosy: user id is empty")
	}
	if creds.AuthToken == "" {
		return nil, fmt.Errorf("cosy: auth token is empty")
	}

	aesKey, err := generateAESKey()
	if err != nil {
		return nil, err
	}
	cosyKey, info, err := encryptUserInfo(aesKey, creds)
	if err != nil {
		return nil, err
	}

	requestID, err := newUUID()
	if err != nil {
		return nil, err
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	payload := cosyPayload{
		Version:     "v1",
		RequestID:   requestID,
		Info:        info,
		CosyVersion: ideVersion,
		IDEVersion:  "",
	}
	payloadJSON, err := marshalNoEscape(payload)
	if err != nil {
		return nil, fmt.Errorf("cosy: encode payload: %w", err)
	}
	payloadB64 := base64.StdEncoding.EncodeToString(payloadJSON)

	sigPath := computeSigPath(requestURL)
	// Go strings are byte sequences, so plain concatenation preserves the body
	// bytes exactly — the reference builds the same string with latin1, which
	// is the identity mapping for bytes 0..255.
	sigInput := payloadB64 + "\n" + cosyKey + "\n" + timestamp + "\n" + string(body) + "\n" + sigPath
	sig := fmt.Sprintf("%x", md5.Sum([]byte(sigInput)))
	bodyHash := fmt.Sprintf("%x", md5.Sum(body))

	machineID := creds.MachineID
	if machineID == "" {
		machineID, err = newUUID()
		if err != nil {
			return nil, err
		}
	}
	headerRequestID, err := newUUID()
	if err != nil {
		return nil, err
	}

	h := http.Header{}
	h.Set("Authorization", "Bearer COSY."+payloadB64+"."+sig)
	h.Set("Cosy-Key", cosyKey)
	h.Set("Cosy-User", creds.UserID)
	h.Set("Cosy-Date", timestamp)
	h.Set("Cosy-Version", ideVersion)
	h.Set("Cosy-Machineid", machineID)
	h.Set("Cosy-Machinetoken", machineID)
	h.Set("Cosy-Machinetype", machineType)
	h.Set("Cosy-Machineos", machineOS)
	h.Set("Cosy-Clienttype", clientType)
	h.Set("Cosy-Clientip", "127.0.0.1")
	h.Set("Cosy-Bodyhash", bodyHash)
	h.Set("Cosy-Bodylength", fmt.Sprintf("%d", len(body)))
	h.Set("Cosy-Sigpath", sigPath)
	h.Set("Cosy-Data-Policy", dataPolicy)
	// Organization identity is empty for personal accounts, but the headers
	// must still be present — the signature check expects them.
	h.Set("Cosy-Organization-Id", "")
	h.Set("Cosy-Organization-Tags", "")
	h.Set("Login-Version", loginVersion)
	h.Set("X-Request-Id", headerRequestID)
	return h, nil
}

// cosyUserInfo is the plaintext encrypted into the Authorization payload. Field
// order must match the reference (`uid`, `security_oauth_token`, `name`, `aid`,
// `email`).
type cosyUserInfo struct {
	UserID             string `json:"uid"`
	SecurityOAuthToken string `json:"security_oauth_token"`
	Name               string `json:"name"`
	Aid                string `json:"aid"`
	Email              string `json:"email"`
}

// cosyPayload is the base64-wrapped signing payload. Field order must match the
// reference (`version`, `requestId`, `info`, `cosyVersion`, `ideVersion`).
type cosyPayload struct {
	Version     string `json:"version"`
	RequestID   string `json:"requestId"`
	Info        string `json:"info"`
	CosyVersion string `json:"cosyVersion"`
	IDEVersion  string `json:"ideVersion"`
}

// generateAESKey returns the first 16 characters of a fresh UUIDv4 canonical
// string (hyphens included). 16 ASCII bytes == AES-128 key.
func generateAESKey() (string, error) {
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	return id[:16], nil
}

// encryptUserInfo returns (cosyKey, info): the RSA-wrapped AES key and the
// base64 AES-CBC ciphertext of the user-info JSON.
func encryptUserInfo(aesKey string, creds Creds) (cosyKey, info string, err error) {
	plaintext, err := marshalNoEscape(cosyUserInfo{
		UserID:             creds.UserID,
		SecurityOAuthToken: creds.AuthToken,
		Name:               creds.Name,
		Aid:                "",
		Email:              creds.Email,
	})
	if err != nil {
		return "", "", fmt.Errorf("cosy: encode user info: %w", err)
	}

	keyBytes := []byte(aesKey)
	if len(keyBytes) != 16 {
		return "", "", fmt.Errorf("cosy: aes key must be 16 bytes, got %d", len(keyBytes))
	}
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return "", "", fmt.Errorf("cosy: aes cipher: %w", err)
	}
	padded := pkcs7Pad(plaintext, block.BlockSize())
	out := make([]byte, len(padded))
	// The reference reuses the key bytes as the IV. The key is fresh per
	// request, so the IV is unique per request as well.
	cipher.NewCBCEncrypter(block, keyBytes[:block.BlockSize()]).CryptBlocks(out, padded)
	info = base64.StdEncoding.EncodeToString(out)

	pub, err := qoderPublicKey()
	if err != nil {
		return "", "", err
	}
	wrapped, err := rsa.EncryptPKCS1v15(rand.Reader, pub, keyBytes)
	if err != nil {
		return "", "", fmt.Errorf("cosy: rsa encrypt: %w", err)
	}
	cosyKey = base64.StdEncoding.EncodeToString(wrapped)
	return cosyKey, info, nil
}

// pkcs7Pad always appends 1..blockSize bytes, matching the reference (a full
// block is added when the input is already aligned).
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padded := make([]byte, len(data)+padding)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	return padded
}

// computeSigPath strips a leading "/algo" from the URL pathname, matching
// qodercli. Unparseable URLs sign an empty path.
func computeSigPath(requestURL string) string {
	u, err := url.Parse(requestURL)
	if err != nil {
		return ""
	}
	path := u.Path
	if strings.HasPrefix(path, "/algo") {
		return path[len("/algo"):]
	}
	return path
}

var (
	rsaKeyOnce sync.Once
	rsaKeyPub  *rsa.PublicKey
	rsaKeyErr  error
)

// qoderPublicKey parses the embedded COSY RSA public key once.
func qoderPublicKey() (*rsa.PublicKey, error) {
	rsaKeyOnce.Do(func() {
		block, _ := pem.Decode([]byte(qoderRSAPublicKey))
		if block == nil {
			rsaKeyErr = fmt.Errorf("cosy: embedded RSA public key is not PEM")
			return
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			rsaKeyErr = fmt.Errorf("cosy: parse RSA public key: %w", err)
			return
		}
		pub, ok := parsed.(*rsa.PublicKey)
		if !ok {
			rsaKeyErr = fmt.Errorf("cosy: embedded key is not RSA")
			return
		}
		rsaKeyPub = pub
	})
	return rsaKeyPub, rsaKeyErr
}

// newUUID delegates to the device-flow helper so the whole repo shares one
// RFC 4122 v4 implementation.
func newUUID() (string, error) {
	return deviceflow.NewUUIDv4()
}
