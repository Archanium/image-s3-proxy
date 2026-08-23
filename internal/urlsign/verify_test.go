package urlsign

import (
	"errors"
	"strings"
	"testing"
)

// Hex encodings of the plaintext key "secret" and salt "hello", matching
// imgproxy's documented example.
const (
	testKeyHex  = "736563726574"
	testSaltHex = "68656c6c6f"
)

// referenceVectors are computed from imgproxy's DOCUMENTED algorithm —
// HMAC-SHA256 over salt ‖ path, truncated, then unpadded URL-safe base64 —
// using an independent implementation, NOT this package. If a change here
// makes these fail, this service has stopped being signature-compatible
// with imgproxy's client SDKs, which is the whole point of the format.
var referenceVectors = []struct {
	name    string
	keyHex  string
	saltHex string
	size    int
	path    string
	want    string
}{
	{
		// Verbatim from the imgproxy signing documentation.
		name: "imgproxy documentation example", keyHex: testKeyHex, saltHex: testSaltHex, size: 32,
		path: "/rs:fill:300:400:0/g:sm/aHR0cDovL2V4YW1w/bGUuY29tL2ltYWdl/cy9jdXJpb3NpdHku/anBn.png",
		want: "oKfUtW34Dvo2BGQehJFR4Nr0_rIjOtdtzJ3QFsUcXH8",
	},
	{
		name: "this service's URL shape", keyHex: testKeyHex, saltHex: testSaltHex, size: 32,
		path: "/rs:fill:240:336:1/bg:fff/13/products/foo.jpg",
		want: "9edIbbeThZB1Mj9UDXIKkLZrosHudcDwb8Bf34HdQ2o",
	},
	{
		name: "no processing options", keyHex: testKeyHex, saltHex: testSaltHex, size: 32,
		path: "/13/products/foo.jpg",
		want: "MDsxnoOls_8OgJ5GadmRnFbwBaOQ2PYISGejKi6wYu4",
	},
	{
		name: "truncated to 8 bytes", keyHex: testKeyHex, saltHex: testSaltHex, size: 8,
		path: "/rs:fill:240:336:1/bg:fff/13/products/foo.jpg",
		want: "9edIbbeThZA",
	},
	{
		name: "different salt yields a different signature", keyHex: testKeyHex, saltHex: "776f726c64", size: 32,
		path: "/rs:fill:240:336:1/bg:fff/13/products/foo.jpg",
		want: "oyxb1i32pTFW8RgnnF_EYGeD7-3bG4tKxnfOegeUgNM",
	},
}

func newTestVerifier(t *testing.T, size int, allowUnsafe bool) *Verifier {
	t.Helper()
	v, err := NewVerifier(testKeyHex, testSaltHex, size, allowUnsafe)
	if err != nil {
		t.Fatalf("NewVerifier returned unexpected error: %v", err)
	}
	return v
}

func TestVerify_ReferenceVectors(t *testing.T) {
	for _, tt := range referenceVectors {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			v, err := NewVerifier(tt.keyHex, tt.saltHex, tt.size, false)
			if err != nil {
				t.Fatalf("NewVerifier returned unexpected error: %v", err)
			}
			if err := v.Verify(tt.want, tt.path); err != nil {
				t.Errorf("Verify(%q, %q) = %v, want nil", tt.want, tt.path, err)
			}
		})
	}
}

// TestSign_MatchesReferenceVectors pins Sign against the same externally
// derived vectors, so Sign and Verify cannot drift together into a
// self-consistent but imgproxy-incompatible pair.
func TestSign_MatchesReferenceVectors(t *testing.T) {
	for _, tt := range referenceVectors {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			v, err := NewVerifier(tt.keyHex, tt.saltHex, tt.size, false)
			if err != nil {
				t.Fatalf("NewVerifier returned unexpected error: %v", err)
			}
			got, err := v.Sign(tt.path)
			if err != nil {
				t.Fatalf("Sign returned unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Sign(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestVerify_Rejects(t *testing.T) {
	const path = "/rs:fill:240:336:1/bg:fff/13/products/foo.jpg"
	const valid = "9edIbbeThZB1Mj9UDXIKkLZrosHudcDwb8Bf34HdQ2o"

	tests := []struct {
		name      string
		keyHex    string
		saltHex   string
		signature string
		path      string
	}{
		{"wrong key", "0badc0de", testSaltHex, valid, path},
		{"wrong salt", testKeyHex, "776f726c64", valid, path},
		{"tampered option", testKeyHex, testSaltHex, valid, "/rs:fill:2400:336:1/bg:fff/13/products/foo.jpg"},
		{"tampered source", testKeyHex, testSaltHex, valid, "/rs:fill:240:336:1/bg:fff/13/products/bar.jpg"},
		{"reordered options", testKeyHex, testSaltHex, valid, "/bg:fff/rs:fill:240:336:1/13/products/foo.jpg"},
		{"missing leading slash", testKeyHex, testSaltHex, valid, strings.TrimPrefix(path, "/")},
		{"truncated signature", testKeyHex, testSaltHex, valid[:20], path},
		{"signature with an extra character", testKeyHex, testSaltHex, valid + "A", path},
		{"empty signature", testKeyHex, testSaltHex, "", path},
		{"not base64 at all", testKeyHex, testSaltHex, "!!!not base64!!!", path},
		{"signature of a different path", testKeyHex, testSaltHex, "MDsxnoOls_8OgJ5GadmRnFbwBaOQ2PYISGejKi6wYu4", path},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			v, err := NewVerifier(tt.keyHex, tt.saltHex, 0, false)
			if err != nil {
				t.Fatalf("NewVerifier returned unexpected error: %v", err)
			}
			if err := v.Verify(tt.signature, tt.path); !errors.Is(err, ErrSignatureMismatch) {
				t.Errorf("Verify = %v, want ErrSignatureMismatch", err)
			}
		})
	}
}

// TestVerify_TruncationAppliesToBothSides guards against a verifier that
// truncates the expected digest but compares against an untruncated
// signature, or vice versa.
func TestVerify_TruncationAppliesToBothSides(t *testing.T) {
	const path = "/rs:fill:240:336:1/bg:fff/13/products/foo.jpg"
	const full = "9edIbbeThZB1Mj9UDXIKkLZrosHudcDwb8Bf34HdQ2o"
	const short = "9edIbbeThZA"

	shortV := newTestVerifier(t, 8, false)
	if err := shortV.Verify(short, path); err != nil {
		t.Errorf("8-byte verifier rejected its own-size signature: %v", err)
	}
	if err := shortV.Verify(full, path); !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("8-byte verifier accepted a 32-byte signature: %v", err)
	}

	fullV := newTestVerifier(t, 32, false)
	if err := fullV.Verify(short, path); !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("32-byte verifier accepted an 8-byte signature: %v", err)
	}
}

func TestVerify_AcceptsPaddedSignature(t *testing.T) {
	const path = "/13/products/foo.jpg"
	v := newTestVerifier(t, 8, false)
	sig, err := v.Sign(path)
	if err != nil {
		t.Fatalf("Sign returned unexpected error: %v", err)
	}
	// An 8-byte digest base64s to 11 unpadded characters, so a client that
	// pads would send one "=".
	if err := v.Verify(sig+"=", path); err != nil {
		t.Errorf("Verify rejected a padded signature %q: %v", sig+"=", err)
	}
}

func TestVerify_Unsafe(t *testing.T) {
	const path = "/w:240/13/products/foo.jpg"

	t.Run("rejected when not allowed", func(t *testing.T) {
		v := newTestVerifier(t, 0, false)
		if err := v.Verify(UnsafeSignature, path); !errors.Is(err, ErrUnsafeNotAllowed) {
			t.Errorf("Verify = %v, want ErrUnsafeNotAllowed", err)
		}
	})

	t.Run("accepted when allowed", func(t *testing.T) {
		v := newTestVerifier(t, 0, true)
		if err := v.Verify(UnsafeSignature, path); err != nil {
			t.Errorf("Verify = %v, want nil", err)
		}
	})

	t.Run("real signatures still verify when unsafe is allowed", func(t *testing.T) {
		v := newTestVerifier(t, 0, true)
		sig, err := v.Sign(path)
		if err != nil {
			t.Fatalf("Sign returned unexpected error: %v", err)
		}
		if err := v.Verify(sig, path); err != nil {
			t.Errorf("Verify = %v, want nil", err)
		}
	})

	t.Run("bad signatures are still rejected when unsafe is allowed", func(t *testing.T) {
		v := newTestVerifier(t, 0, true)
		if err := v.Verify("UNSAFE", path); !errors.Is(err, ErrSignatureMismatch) {
			t.Errorf("Verify(%q) = %v, want ErrSignatureMismatch (the literal is case-sensitive)", "UNSAFE", err)
		}
	})
}

func TestVerify_UnsignedOnlyMode(t *testing.T) {
	v, err := NewVerifier("", "", 0, true)
	if err != nil {
		t.Fatalf("NewVerifier returned unexpected error: %v", err)
	}
	if err := v.Verify(UnsafeSignature, "/13/products/foo.jpg"); err != nil {
		t.Errorf("Verify(unsafe) = %v, want nil", err)
	}
	// With no key there is no signature that can ever verify.
	if err := v.Verify("9edIbbeThZB1Mj9UDXIKkLZrosHudcDwb8Bf34HdQ2o", "/13/products/foo.jpg"); !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("Verify(signature) = %v, want ErrSignatureMismatch", err)
	}
	if _, err := v.Sign("/13/products/foo.jpg"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Sign = %v, want ErrNotConfigured", err)
	}
}

// TestVerify_NilVerifierFailsClosed guards the route-disabled path: a nil
// verifier must never accept anything, including "unsafe".
func TestVerify_NilVerifierFailsClosed(t *testing.T) {
	var v *Verifier
	if err := v.Verify(UnsafeSignature, "/13/products/foo.jpg"); !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("nil Verify(unsafe) = %v, want ErrSignatureMismatch", err)
	}
	if err := v.Verify("anything", "/13/products/foo.jpg"); !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("nil Verify = %v, want ErrSignatureMismatch", err)
	}
	if v.AllowsUnsafe() {
		t.Error("nil AllowsUnsafe() = true, want false")
	}
	if _, err := v.Sign("/x"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("nil Sign = %v, want ErrNotConfigured", err)
	}
}

func TestNewVerifier_Configuration(t *testing.T) {
	tests := []struct {
		name        string
		keyHex      string
		saltHex     string
		size        int
		allowUnsafe bool
		wantErr     string
		wantIs      error
	}{
		{name: "valid", keyHex: testKeyHex, saltHex: testSaltHex},
		{name: "valid with truncation", keyHex: testKeyHex, saltHex: testSaltHex, size: 16},
		{name: "valid with max size", keyHex: testKeyHex, saltHex: testSaltHex, size: 32},
		{name: "whitespace is trimmed", keyHex: " " + testKeyHex + "\n", saltHex: "\t" + testSaltHex + " "},
		{name: "unset and unsafe disabled", wantIs: ErrNotConfigured},
		{name: "unset but unsafe enabled", allowUnsafe: true},
		{name: "key without salt", keyHex: testKeyHex, wantErr: "both"},
		{name: "salt without key", saltHex: testSaltHex, wantErr: "both"},
		{name: "key not hex", keyHex: "zzzz", saltHex: testSaltHex, wantErr: "key is not valid hex"},
		{name: "salt not hex", keyHex: testKeyHex, saltHex: "zzzz", wantErr: "salt is not valid hex"},
		{name: "size too large", keyHex: testKeyHex, saltHex: testSaltHex, size: 33, wantErr: "out of range"},
		{name: "size negative", keyHex: testKeyHex, saltHex: testSaltHex, size: -1, wantErr: "out of range"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			v, err := NewVerifier(tt.keyHex, tt.saltHex, tt.size, tt.allowUnsafe)
			switch {
			case tt.wantIs != nil:
				if !errors.Is(err, tt.wantIs) {
					t.Fatalf("err = %v, want %v", err, tt.wantIs)
				}
				if v != nil {
					t.Error("verifier is non-nil on error")
				}
			case tt.wantErr != "":
				if err == nil {
					t.Fatalf("err = nil, want an error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %q, want it to contain %q", err.Error(), tt.wantErr)
				}
				if v != nil {
					t.Error("verifier is non-nil on error")
				}
			default:
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if v == nil {
					t.Fatal("verifier is nil without an error")
				}
			}
		})
	}
}

// TestNewVerifier_NeverLeaksKeyMaterial guards the "never log credentials"
// guardrail at its most likely leak point: a construction error.
func TestNewVerifier_NeverLeaksKeyMaterial(t *testing.T) {
	const secretish = "deadbeefcafe"
	_, err := NewVerifier(secretish, "zzzz", 0, false)
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if strings.Contains(err.Error(), secretish) {
		t.Errorf("error %q leaks the key material", err.Error())
	}
}
