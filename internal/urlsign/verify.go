// Package urlsign verifies imgproxy-compatible URL signatures.
//
// Story: URL Signature Verification
//
// Input:  a signature segment taken from a /_p/ URL, and the path that
//
//	follows it (the processing options plus the source tail, with a
//	leading slash).
//
// Process:
//  1. Short-circuit the literal "unsafe" signature when the deployment
//     explicitly permits unsigned URLs.
//  2. Compute HMAC-SHA256 over salt ‖ path using the configured key.
//  3. Truncate the digest to the configured size.
//  4. Compare against the decoded signature in constant time.
//
// Output: nil when the signature is valid, a sentinel error otherwise.
//
// Dependencies: stdlib only.
// Side effects: none. Nothing here logs, and no error ever embeds key
// material.
//
// The signed payload deliberately EXCLUDES the "/_p/{signature}" prefix, so
// what gets signed is exactly "/{options}/{source}" — the same bytes an
// off-the-shelf imgproxy signing SDK produces. That is the compatibility
// contract for this package; see verify_test.go, whose vectors come from the
// documented imgproxy algorithm rather than from this implementation.
package urlsign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	// UnsafeSignature is the literal signature segment that bypasses
	// verification, and only where the deployment opts in.
	UnsafeSignature = "unsafe"
	// DefaultSignatureSize is the full SHA-256 digest length in bytes.
	DefaultSignatureSize = 32
	// MaxSignatureSize is the largest meaningful truncation: a SHA-256
	// digest is 32 bytes, so asking for more is a configuration error.
	MaxSignatureSize = sha256.Size
)

var (
	// ErrNotConfigured reports that neither signing keys nor unsafe URLs
	// were configured, so the signed-URL feature is disabled entirely.
	// Callers treat this as "leave the route switched off", not as a
	// startup failure.
	ErrNotConfigured = errors.New("url signing is not configured")
	// ErrSignatureMismatch reports that the supplied signature did not
	// match the expected one. It is deliberately indistinguishable across
	// wrong-key, tampered-path, and malformed-signature cases.
	ErrSignatureMismatch = errors.New("signature mismatch")
	// ErrUnsafeNotAllowed reports that a URL used the literal "unsafe"
	// signature on a deployment that does not permit unsigned URLs. It is
	// separate from ErrSignatureMismatch only so operators can tell a
	// misconfiguration from an attack in the logs; both yield 403.
	ErrUnsafeNotAllowed = errors.New("unsigned URLs are not allowed")
)

// Verifier checks imgproxy-compatible URL signatures. The zero value is not
// usable; construct one with NewVerifier. A nil *Verifier fails closed —
// every Verify call on it returns ErrSignatureMismatch.
type Verifier struct {
	key         []byte
	salt        []byte
	size        int
	allowUnsafe bool
}

// NewVerifier builds a Verifier from hex-encoded key and salt, matching
// imgproxy's IMGPROXY_KEY / IMGPROXY_SALT encoding.
//
// size is the digest truncation in bytes; 0 selects the full 32-byte digest.
// allowUnsafe permits the literal "unsafe" signature, which is intended for
// local development only.
//
// Both keyHex and saltHex must be supplied together. When both are empty and
// allowUnsafe is false, the feature is off and ErrNotConfigured is returned
// so the caller can leave the route disabled.
func NewVerifier(keyHex, saltHex string, size int, allowUnsafe bool) (*Verifier, error) {
	keyHex = strings.TrimSpace(keyHex)
	saltHex = strings.TrimSpace(saltHex)

	if (keyHex == "") != (saltHex == "") {
		return nil, errors.New("key and salt must both be set, or both be empty")
	}
	if keyHex == "" {
		if allowUnsafe {
			// Unsigned-only mode: the route is reachable, but exclusively
			// via the literal "unsafe" signature.
			return &Verifier{size: DefaultSignatureSize, allowUnsafe: true}, nil
		}
		return nil, ErrNotConfigured
	}

	// Decode without ever echoing the material back in an error.
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, errors.New("key is not valid hex")
	}
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return nil, errors.New("salt is not valid hex")
	}
	if len(key) == 0 {
		return nil, errors.New("key decodes to zero bytes")
	}

	if size == 0 {
		size = DefaultSignatureSize
	}
	if size < 1 || size > MaxSignatureSize {
		return nil, fmt.Errorf("signature size %d out of range (expected 1-%d)", size, MaxSignatureSize)
	}

	return &Verifier{key: key, salt: salt, size: size, allowUnsafe: allowUnsafe}, nil
}

// AllowsUnsafe reports whether this verifier accepts the literal "unsafe"
// signature. Used at startup to decide whether to warn the operator.
func (v *Verifier) AllowsUnsafe() bool {
	return v != nil && v.allowUnsafe
}

// digest computes the truncated HMAC for path.
func (v *Verifier) digest(path string) []byte {
	mac := hmac.New(sha256.New, v.key)
	mac.Write(v.salt)
	mac.Write([]byte(path))
	return mac.Sum(nil)[:v.size]
}

// Sign produces the signature for path, where path is "/{options}/{source}"
// — everything after the "/_p/{signature}" prefix, including the leading
// slash. It is the inverse of Verify and exists for tooling and tests.
func (v *Verifier) Sign(path string) (string, error) {
	if v == nil || len(v.key) == 0 {
		return "", ErrNotConfigured
	}
	return base64.RawURLEncoding.EncodeToString(v.digest(path)), nil
}

// Verify checks signature against path. path must be "/{options}/{source}"
// — everything after the "/_p/{signature}" prefix, including the leading
// slash.
//
// A nil receiver, an unconfigured key, or a malformed signature all fail
// closed.
func (v *Verifier) Verify(signature, path string) error {
	if v == nil {
		return ErrSignatureMismatch
	}
	if signature == UnsafeSignature {
		if v.allowUnsafe {
			return nil
		}
		return ErrUnsafeNotAllowed
	}
	if len(v.key) == 0 {
		// Unsigned-only mode: nothing but "unsafe" can ever verify.
		return ErrSignatureMismatch
	}

	provided, err := decodeSignature(signature)
	if err != nil {
		return ErrSignatureMismatch
	}
	if !hmac.Equal(provided, v.digest(path)) {
		return ErrSignatureMismatch
	}
	return nil
}

// decodeSignature accepts URL-safe base64 with or without padding. imgproxy
// emits the unpadded form; tolerating padding costs nothing and avoids a
// class of client bug that is otherwise invisible.
func decodeSignature(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}
