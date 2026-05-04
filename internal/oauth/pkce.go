package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
)

var pkceRandRead = rand.Read

func GenerateState() (string, error) {
	return generateRandomPKCEToken("oauth.GenerateState")
}

func GenerateCodeVerifier() (string, error) {
	return generateRandomPKCEToken("oauth.GenerateCodeVerifier")
}

func CodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func CompareStatesConstantTime(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func generateRandomPKCEToken(op string) (string, error) {
	buf := make([]byte, 32)

	n, err := pkceRandRead(buf)
	if err != nil {
		return "", fmt.Errorf("%s: read crypto/rand: %w", op, err)
	}
	if n != len(buf) {
		return "", fmt.Errorf("%s: read crypto/rand: %w", op, io.ErrUnexpectedEOF)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}
