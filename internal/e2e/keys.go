package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const SignKeyLen = ed25519.PublicKeySize

type KeyPair struct {
	SignPrivate []byte
	SignPublic  []byte
}

func Generate() (*KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519: %w", err)
	}
	return &KeyPair{
		SignPrivate: append([]byte(nil), priv.Seed()...),
		SignPublic:  append([]byte(nil), pub...),
	}, nil
}

func (kp *KeyPair) PrivateKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(kp.SignPrivate)
}

func (kp *KeyPair) Fingerprint() string {
	return Fingerprint(kp.SignPublic)
}

func Fingerprint(pub []byte) string {
	sum := sha256.Sum256(pub)
	return "ed25519:" + base64.RawStdEncoding.EncodeToString(sum[:16])
}

func PubEquals(a, b []byte) bool {
	if len(a) != len(b) || len(a) != SignKeyLen {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

type keyPairJSON struct {
	SignPrivate string `json:"sign_private"`
	SignPublic  string `json:"sign_public"`
}

func (kp *KeyPair) MarshalJSON() ([]byte, error) {
	return json.Marshal(keyPairJSON{
		SignPrivate: base64.StdEncoding.EncodeToString(kp.SignPrivate),
		SignPublic:  base64.StdEncoding.EncodeToString(kp.SignPublic),
	})
}

func (kp *KeyPair) UnmarshalJSON(data []byte) error {
	var raw keyPairJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	priv, err := base64.StdEncoding.DecodeString(raw.SignPrivate)
	if err != nil {
		return fmt.Errorf("decode sign_private: %w", err)
	}
	pub, err := base64.StdEncoding.DecodeString(raw.SignPublic)
	if err != nil {
		return fmt.Errorf("decode sign_public: %w", err)
	}
	if len(priv) != ed25519.SeedSize {
		return fmt.Errorf("bad sign_private length: %d", len(priv))
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("bad sign_public length: %d", len(pub))
	}
	kp.SignPrivate = priv
	kp.SignPublic = pub
	return nil
}

func LoadOrCreate(path string) (*KeyPair, error) {
	if data, err := os.ReadFile(path); err == nil {
		var kp KeyPair
		if err := json.Unmarshal(data, &kp); err == nil {
			return &kp, nil
		}
	}
	kp, err := Generate()
	if err != nil {
		return nil, err
	}
	if err := Save(path, kp); err != nil {
		return nil, err
	}
	return kp, nil
}

func Save(path string, kp *KeyPair) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(kp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}