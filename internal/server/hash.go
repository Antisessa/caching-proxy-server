package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// Hash function sum request params:
//   - full URL
//   - Request method
//   - Headers and its values
//   - Request Body
//
// return hash sum as string
func Hash(fullOrigin, method string, headers http.Header, bodyBytes []byte) string {
	hash := sha256.New()
	hash.Write([]byte(fullOrigin))
	hash.Write([]byte(method))

	// Headers
	for header, values := range headers {
		hash.Write([]byte(header))
		for _, value := range values {
			hash.Write([]byte(value))
		}
	}

	hash.Write(bodyBytes)
	return hex.EncodeToString(hash.Sum(nil))
}
