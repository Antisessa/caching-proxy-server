package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
)

// Hash function sum request params:
//   - full URL
//   - Request method
//   - Headers and its values
//   - Request Body
//
// return hash sum as string
// return error while trying read request body,
func Hash(r http.Request) (string, error) {
	hash := sha256.New()
	hash.Write([]byte(r.URL.String()))
	hash.Write([]byte(r.Method))

	// Headers
	for header, values := range r.Header {
		hash.Write([]byte(header))
		for _, value := range values {
			hash.Write([]byte(value))
		}
	}

	// Body — читаем БЕЗ потребления!
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // лимит 1MB
	if err != nil {
		return "", err
	}

	// ВОССТАНАВЛИВАЕМ body!
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	hash.Write(bodyBytes)
	return hex.EncodeToString(hash.Sum(nil)), nil
}
