package token

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/pbkdf2"

	"peerdrop-awg/internal/wgconfig"
)

const (
	prefix     = "PDROPv1:"
	saltLen    = 16
	keyLen     = 32
	iterations = 100_000
)

type envelope struct {
	Encrypted bool                   `json:"enc"`
	Config    *wgconfig.ClientConfig `json:"config,omitempty"`

	Salt  string `json:"salt,omitempty"`
	Nonce string `json:"nonce,omitempty"`
	Data  string `json:"data,omitempty"`
}

func Encode(cfg *wgconfig.ClientConfig, password string) (string, error) {
	if cfg == nil {
		return "", errors.New("empty config")
	}

	if password == "" {
		env := envelope{Encrypted: false, Config: cfg}
		raw, err := json.Marshal(env)
		if err != nil {
			return "", err
		}
		return prefix + base64.StdEncoding.EncodeToString(raw), nil
	}

	plaintext, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	key := pbkdf2.Key([]byte(password), salt, iterations, keyLen, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	env := envelope{
		Encrypted: true,
		Salt:      base64.StdEncoding.EncodeToString(salt),
		Nonce:     base64.StdEncoding.EncodeToString(nonce),
		Data:      base64.StdEncoding.EncodeToString(ciphertext),
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return prefix + base64.StdEncoding.EncodeToString(raw), nil
}

func Decode(tok, password string) (*wgconfig.ClientConfig, error) {
	tok = strings.TrimSpace(tok)
	tok = strings.ReplaceAll(tok, "\n", "")
	tok = strings.ReplaceAll(tok, "\r", "")
	tok = strings.ReplaceAll(tok, " ", "")

	if !strings.HasPrefix(tok, prefix) {
		return nil, errors.New("invalid token format (expected PDROPv1:…)")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(tok, prefix))
	if err != nil {
		return nil, fmt.Errorf("base64: %w", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}

	if !env.Encrypted {
		if env.Config == nil {
			return nil, errors.New("token has no config")
		}
		return env.Config, nil
	}

	if password == "" {
		return nil, errors.New("token is encrypted — password required")
	}
	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil {
		return nil, fmt.Errorf("salt: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return nil, fmt.Errorf("data: %w", err)
	}

	key := pbkdf2.Key([]byte(password), salt, iterations, keyLen, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, errors.New("wrong password or corrupted token")
	}

	var cfg wgconfig.ClientConfig
	if err := json.Unmarshal(plaintext, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}