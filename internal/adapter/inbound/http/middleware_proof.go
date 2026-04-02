package http

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/port"
	"github.com/alkem-io/wopi-service/internal/domain/service"
)

const proofTimestampMaxAge = 20 * time.Minute

// ProofMiddleware validates WOPI proof signatures from Collabora.
// When enabled, it verifies X-WOPI-Proof headers using RSA SHA-256
// with public keys from WOPI discovery. When disabled, it passes through.
func ProofMiddleware(enabled bool, discoverySvc *service.DiscoveryService, logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled {
				next.ServeHTTP(w, r)
				return
			}

			proofKeys := discoverySvc.GetProofKeys()
			if proofKeys == nil {
				logger.Warn("proof validation enabled but no discovery keys available")
				http.Error(w, `{"error":"proof validation unavailable"}`, http.StatusInternalServerError)
				return
			}

			if err := validateProof(r, proofKeys); err != nil {
				logger.Warn("WOPI proof validation failed", zap.Error(err))
				http.Error(w, `{"error":"invalid proof"}`, http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func validateProof(r *http.Request, keys *port.ProofKey) error {
	proof := r.Header.Get("X-WOPI-Proof")
	proofOld := r.Header.Get("X-WOPI-ProofOld")
	timestampStr := r.Header.Get("X-WOPI-TimeStamp")

	if proof == "" || timestampStr == "" {
		return errMissingProofHeaders
	}

	// Validate timestamp freshness (20 minute window)
	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return errInvalidTimestamp
	}
	if !isTimestampFresh(timestamp) {
		return errTimestampExpired
	}

	// Build the expected proof payload
	accessToken := r.URL.Query().Get("access_token")
	fullURL := strings.ToUpper(requestURL(r))
	expected := buildProofPayload(accessToken, fullURL, timestamp)

	// Decode proof signatures
	proofBytes, err := base64.StdEncoding.DecodeString(proof)
	if err != nil {
		return errInvalidProofEncoding
	}

	// Build RSA public keys from discovery modulus/exponent
	currentKey, err := buildRSAKey(keys.Modulus, keys.Exponent)
	if err != nil {
		return err
	}

	// Try: current proof with current key
	if verifyRSASHA256(currentKey, expected, proofBytes) {
		return nil
	}

	// Try: old proof with current key (if available)
	if proofOld != "" {
		proofOldBytes, err := base64.StdEncoding.DecodeString(proofOld)
		if err == nil && verifyRSASHA256(currentKey, expected, proofOldBytes) {
			return nil
		}
	}

	// Try: current proof with old key (if available)
	if keys.OldModulus != "" && keys.OldExponent != "" {
		oldKey, err := buildRSAKey(keys.OldModulus, keys.OldExponent)
		if err == nil && verifyRSASHA256(oldKey, expected, proofBytes) {
			return nil
		}
	}

	return errProofVerificationFailed
}

func buildProofPayload(accessToken, urlUpper string, timestamp int64) []byte {
	tokenBytes := []byte(accessToken)
	urlBytes := []byte(urlUpper)

	// Payload: [4 bytes token len][token][4 bytes url len][url][4 bytes ts len][8 bytes ts]
	buf := make([]byte, 0, 4+len(tokenBytes)+4+len(urlBytes)+4+8)

	buf = appendBigEndianUint32(buf, uint32(len(tokenBytes))) //nolint:gosec // length fits in uint32
	buf = append(buf, tokenBytes...)
	buf = appendBigEndianUint32(buf, uint32(len(urlBytes))) //nolint:gosec // length fits in uint32
	buf = append(buf, urlBytes...)
	buf = appendBigEndianUint32(buf, 8)
	buf = appendBigEndianUint64(buf, uint64(timestamp)) //nolint:gosec // WOPI ticks are always positive

	return buf
}

func buildRSAKey(modulusB64, exponentB64 string) (*rsa.PublicKey, error) {
	modBytes, err := base64.StdEncoding.DecodeString(modulusB64)
	if err != nil {
		return nil, errInvalidKeyEncoding
	}
	expBytes, err := base64.StdEncoding.DecodeString(exponentB64)
	if err != nil {
		return nil, errInvalidKeyEncoding
	}

	modulus := new(big.Int).SetBytes(modBytes)
	exponent := new(big.Int).SetBytes(expBytes)

	if !exponent.IsInt64() || exponent.Int64() <= 0 {
		return nil, errInvalidKeyEncoding
	}

	return &rsa.PublicKey{
		N: modulus,
		E: int(exponent.Int64()),
	}, nil
}

func verifyRSASHA256(key *rsa.PublicKey, message, signature []byte) bool {
	hash := sha256.Sum256(message)
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], signature) == nil
}

// isTimestampFresh checks if the .NET ticks timestamp is within the allowed window.
// .NET ticks: 100-nanosecond intervals since January 1, 0001.
func isTimestampFresh(ticks int64) bool {
	// Convert .NET ticks to Unix time
	// Difference between .NET epoch (0001-01-01) and Unix epoch (1970-01-01) in ticks
	const unixEpochTicks = 621_355_968_000_000_000

	unixNanos := (ticks - unixEpochTicks) * 100
	proofTime := time.Unix(0, unixNanos)
	skew := time.Since(proofTime)
	if skew < 0 {
		skew = -skew
	}
	return skew <= proofTimestampMaxAge
}

func requestURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "https"
		if r.TLS == nil {
			scheme = "http"
		}
	}
	return scheme + "://" + r.Host + r.URL.RequestURI()
}

func appendBigEndianUint32(buf []byte, v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return append(buf, b...)
}

func appendBigEndianUint64(buf []byte, v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return append(buf, b...)
}

// Proof validation errors.
var (
	errMissingProofHeaders     = proofError("missing X-WOPI-Proof or X-WOPI-TimeStamp headers")
	errInvalidTimestamp        = proofError("invalid X-WOPI-TimeStamp")
	errTimestampExpired        = proofError("X-WOPI-TimeStamp too old")
	errInvalidProofEncoding    = proofError("invalid proof base64 encoding")
	errInvalidKeyEncoding      = proofError("invalid proof key encoding")
	errProofVerificationFailed = proofError("proof signature verification failed")
)

type proofError string

func (e proofError) Error() string { return string(e) }
