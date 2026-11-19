package paginate

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/samber/oops"
)

// AIP-158 suggests 3 days

const (
	tokenExpirationDuration       = int64(3 * 24 * time.Hour / time.Second)
	version                 int64 = 1
)

type pageToken struct {
	Version            int64          `json:"version"`
	CursorValues       map[string]any `json:"cursor_values"`
	SortColumns        []string       `json:"sort_columns"`
	RequestFingerprint string         `json:"request_fingerprint"`
	Timestamp          int64          `json:"timestamp"`
}

func encodeToken(token *pageToken) (string, error) {
	if token == nil {
		return "", nil
	}

	token.Timestamp = time.Now().Unix()
	token.Version = version

	data, err := json.Marshal(token)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(data), nil
}

func decodeToken(tokenStr string) (*pageToken, error) {
	if tokenStr == "" {
		return nil, nil
	}

	data, err := base64.StdEncoding.DecodeString(tokenStr)
	if err != nil {
		return nil, oops.In(ServiceName).Wrapf(err, "decoding token encoding")
	}

	var token pageToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, oops.In(ServiceName).Wrapf(err, "unmarshalling token encoding")
	}

	return &token, nil
}

func generateRequestFingerprint(params *PaginationParams) (string, error) {
	paramsCopy := canonicalFingerprintParams(params)
	data, err := json.Marshal(paramsCopy)
	if err != nil {
		return "", err
	}
	data = append(data, paramsCopy.table+paramsCopy.schema...)
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:]), nil
}

func canonicalFingerprintParams(params *PaginationParams) *PaginationParams {
	if params == nil {
		return nil
	}

	paramsCopy := *params
	paramsCopy.PageToken = ""
	return &paramsCopy
}

func validateToken(token string, params *PaginationParams) error {
	if token == "" {
		return oops.In(ServiceName).New("page token is required for token pagination")
	}

	decodedToken, err := decodeToken(token)
	if err != nil {
		return oops.In(ServiceName).Wrapf(err, "validating page token")
	}

	if err := validateTokenDecoded(decodedToken, params); err != nil {
		return err
	}

	return nil
}

func validateTokenDecoded(token *pageToken, params *PaginationParams) error {
	// validate timestamp
	if time.Now().Unix()-token.Timestamp > tokenExpirationDuration {
		return oops.In(ServiceName).New("token expired")
	}

	// validate version
	if token.Version != version {
		return oops.In(ServiceName).New("invalid token version")
	}

	currentFingerprint, err := generateRequestFingerprint(params)
	if err != nil {
		return oops.In(ServiceName).Wrapf(err, "generating request fingerprint")
	}
	if token.RequestFingerprint != currentFingerprint {
		return oops.In(ServiceName).New("invalid token fingerprint")
	}

	return nil
}

func generateToken(params *PaginationParams, cursorValues map[string]any, sortColumns []string) (string, error) {
	fingerprint, err := generateRequestFingerprint(params)
	if err != nil {
		return "", err
	}
	return encodeToken(&pageToken{
		CursorValues:       cursorValues,
		SortColumns:        sortColumns,
		RequestFingerprint: fingerprint,
	})
}
