package e2e

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

const (
	HandshakeVersion = 1
	MaxFrameSize     = 16 * 1024 * 1024
	sessionInfo      = "peerdrop-session-v1"
)

type helloMsg struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
	Status  string `json:"status,omitempty"`
	Reason  string `json:"reason,omitempty"`
	PeerID  string `json:"peer_id,omitempty"`
	SignPub string `json:"sign_pub,omitempty"`
	EphPub  string `json:"eph_pub,omitempty"`
	Sig     string `json:"sig,omitempty"`
}

func (h *helloMsg) canonBytes(extra string) []byte {
	s := fmt.Sprintf("%s|v%d|%s|%s|%s", h.Type, h.Version, h.PeerID, h.SignPub, h.EphPub)
	if extra != "" {
		s += "|" + extra
	}
	return []byte(s)
}

func (h *helloMsg) verify(signPub []byte) error {
	sig, err := base64.StdEncoding.DecodeString(h.Sig)
	if err != nil {
		return fmt.Errorf("decode sig: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(signPub), h.canonBytes(""), sig) {
		return errors.New("signature mismatch")
	}
	return nil
}

func (h *helloMsg) verifyWithExtra(signPub []byte, extra string) error {
	sig, err := base64.StdEncoding.DecodeString(h.Sig)
	if err != nil {
		return fmt.Errorf("decode sig: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(signPub), h.canonBytes(extra), sig) {
		return errors.New("signature mismatch")
	}
	return nil
}

func generateEphemeral() (pub, priv []byte, err error) {
	priv = make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		return nil, nil, fmt.Errorf("rand: %w", err)
	}
	pub, err = curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, nil, fmt.Errorf("X25519 basepoint: %w", err)
	}
	return pub, priv, nil
}

type SecureConn struct {
	conn         net.Conn
	writeAEAD    interface {
		Seal(dst, nonce, plaintext, ad []byte) []byte
	}
	readAEAD     interface {
		Open(dst, nonce, ciphertext, ad []byte) ([]byte, error)
	}
	writeCounter uint64
	readCounter  uint64
	readBuf      []byte
	readMu       sync.Mutex
	writeMu      sync.Mutex
}

func newSecureConn(conn net.Conn, writeKey, readKey []byte) (*SecureConn, error) {
	wAEAD, err := chacha20poly1305.New(writeKey)
	if err != nil {
		return nil, fmt.Errorf("write AEAD: %w", err)
	}
	rAEAD, err := chacha20poly1305.New(readKey)
	if err != nil {
		return nil, fmt.Errorf("read AEAD: %w", err)
	}
	return &SecureConn{
		conn:      conn,
		writeAEAD: wAEAD,
		readAEAD:  rAEAD,
	}, nil
}

func makeNonce(counter uint64) []byte {
	n := make([]byte, chacha20poly1305.NonceSize)
	binary.BigEndian.PutUint64(n[4:], counter)
	return n
}

func (s *SecureConn) Write(b []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	nonce := makeNonce(s.writeCounter)
	s.writeCounter++
	ct := s.writeAEAD.Seal(nil, nonce, b, nil)

	if len(ct) > MaxFrameSize {
		return 0, fmt.Errorf("frame too large: %d", len(ct))
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(ct)))
	if _, err := s.conn.Write(lenBuf[:]); err != nil {
		return 0, err
	}
	if _, err := s.conn.Write(ct); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (s *SecureConn) Read(b []byte) (int, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()

	if len(s.readBuf) > 0 {
		n := copy(b, s.readBuf)
		s.readBuf = s.readBuf[n:]
		return n, nil
	}

	var lenBuf [4]byte
	if _, err := io.ReadFull(s.conn, lenBuf[:]); err != nil {
		return 0, err
	}
	frameLen := binary.BigEndian.Uint32(lenBuf[:])
	if frameLen > MaxFrameSize {
		return 0, fmt.Errorf("frame too large: %d", frameLen)
	}
	ct := make([]byte, frameLen)
	if _, err := io.ReadFull(s.conn, ct); err != nil {
		return 0, err
	}

	nonce := makeNonce(s.readCounter)
	s.readCounter++
	pt, err := s.readAEAD.Open(nil, nonce, ct, nil)
	if err != nil {
		return 0, fmt.Errorf("decrypt: %w", err)
	}

	n := copy(b, pt)
	if n < len(pt) {
		s.readBuf = pt[n:]
	}
	return n, nil
}

func (s *SecureConn) Close() error                       { return s.conn.Close() }
func (s *SecureConn) RemoteAddr() net.Addr               { return s.conn.RemoteAddr() }
func (s *SecureConn) SetDeadline(t time.Time) error      { return s.conn.SetDeadline(t) }
func (s *SecureConn) SetReadDeadline(t time.Time) error  { return s.conn.SetReadDeadline(t) }
func (s *SecureConn) SetWriteDeadline(t time.Time) error { return s.conn.SetWriteDeadline(t) }

func ClientHandshake(
	conn net.Conn,
	localKeys *KeyPair,
	knownPeers *KnownPeers,
	expectedPeerID string,
	localPeerID string,
) (*SecureConn, error) {
	ephPub, ephPriv, err := generateEphemeral()
	if err != nil {
		return nil, err
	}

	hello := &helloMsg{
		Type:    "hello",
		Version: HandshakeVersion,
		PeerID:  localPeerID,
		SignPub: base64.StdEncoding.EncodeToString(localKeys.SignPublic),
		EphPub:  base64.StdEncoding.EncodeToString(ephPub),
	}
	hello.Sig = base64.StdEncoding.EncodeToString(
		ed25519.Sign(localKeys.PrivateKey(), hello.canonBytes("")),
	)

	w := bufio.NewWriter(conn)
	enc := json.NewEncoder(w)
	if err := enc.Encode(hello); err != nil {
		return nil, fmt.Errorf("write hello: %w", err)
	}
	if err := w.Flush(); err != nil {
		return nil, fmt.Errorf("flush hello: %w", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read hello_resp: %w", err)
	}
	var resp helloMsg
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("parse hello_resp: %w", err)
	}
	if resp.Type != "hello_resp" {
		return nil, fmt.Errorf("unexpected msg type: %q", resp.Type)
	}
	if resp.Status != "ok" {
		return nil, fmt.Errorf("server error: %s", resp.Reason)
	}
	if expectedPeerID != "" && resp.PeerID != expectedPeerID {
		return nil, fmt.Errorf("server identity mismatch: expected %s, got %s",
			expectedPeerID, resp.PeerID)
	}

	respSignPub, err := base64.StdEncoding.DecodeString(resp.SignPub)
	if err != nil {
		return nil, fmt.Errorf("decode server sign_pub: %w", err)
	}
	respEphPub, err := base64.StdEncoding.DecodeString(resp.EphPub)
	if err != nil {
		return nil, fmt.Errorf("decode server eph_pub: %w", err)
	}

	known, matches := knownPeers.Verify(resp.PeerID, respSignPub)
	if known && !matches {
		return nil, fmt.Errorf("server %s public key changed — possible MITM", resp.PeerID)
	}

	extra := string(hello.canonBytes(""))
	if err := resp.verifyWithExtra(respSignPub, extra); err != nil {
		return nil, fmt.Errorf("verify server signature: %w", err)
	}

	if !known {
		if _, _, err := knownPeers.Update(resp.PeerID, "", respSignPub); err != nil {
			return nil, fmt.Errorf("store server pubkey: %w", err)
		}
	}

	shared, err := curve25519.X25519(ephPriv, respEphPub)
	if err != nil {
		return nil, fmt.Errorf("ECDH: %w", err)
	}
	salt := append(append([]byte{}, ephPub...), respEphPub...)
	
	// ИСПРАВЛЕНО: явное распределение ключей
	writeKey, readKey, err := deriveSessionKeys(shared, salt, "client")
	if err != nil {
		return nil, err
	}

	sc, err := newSecureConn(conn, writeKey, readKey)
	if err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
	_ = conn.SetDeadline(time.Time{})
	return sc, nil
}

func ServerHandshake(
	conn net.Conn,
	localKeys *KeyPair,
	knownPeers *KnownPeers,
	localPeerID string,
) (*SecureConn, string, error) {
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, "", fmt.Errorf("read hello: %w", err)
	}
	var hello helloMsg
	if err := json.Unmarshal(line, &hello); err != nil {
		return nil, "", fmt.Errorf("parse hello: %w", err)
	}
	if hello.Type != "hello" || hello.Version != HandshakeVersion {
		return nil, "", fmt.Errorf("unsupported hello (type=%q v=%d)", hello.Type, hello.Version)
	}

	clientSignPub, err := base64.StdEncoding.DecodeString(hello.SignPub)
	if err != nil {
		return nil, "", fmt.Errorf("decode client sign_pub: %w", err)
	}
	clientEphPub, err := base64.StdEncoding.DecodeString(hello.EphPub)
	if err != nil {
		return nil, "", fmt.Errorf("decode client eph_pub: %w", err)
	}

	if err := hello.verify(clientSignPub); err != nil {
		return nil, "", fmt.Errorf("verify client signature: %w", err)
	}

	known, matches := knownPeers.Verify(hello.PeerID, clientSignPub)
	if known && !matches {
		sendError(conn, localPeerID, "public key changed")
		return nil, "", fmt.Errorf("client %s public key changed — possible MITM", hello.PeerID)
	}
	if !known {
		if _, _, err := knownPeers.Update(hello.PeerID, "", clientSignPub); err != nil {
			return nil, "", fmt.Errorf("store client pubkey: %w", err)
		}
	}

	ephPub, ephPriv, err := generateEphemeral()
	if err != nil {
		return nil, "", err
	}
	resp := &helloMsg{
		Type:    "hello_resp",
		Version: HandshakeVersion,
		Status:  "ok",
		PeerID:  localPeerID,
		SignPub: base64.StdEncoding.EncodeToString(localKeys.SignPublic),
		EphPub:  base64.StdEncoding.EncodeToString(ephPub),
	}
	extra := string(hello.canonBytes(""))
	resp.Sig = base64.StdEncoding.EncodeToString(
		ed25519.Sign(localKeys.PrivateKey(), resp.canonBytes(extra)),
	)

	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		return nil, "", fmt.Errorf("write hello_resp: %w", err)
	}

	shared, err := curve25519.X25519(ephPriv, clientEphPub)
	if err != nil {
		return nil, "", fmt.Errorf("ECDH: %w", err)
	}
	salt := append(append([]byte{}, clientEphPub...), ephPub...)
	
	// ИСПРАВЛЕНО: явное распределение ключей
	writeKey, readKey, err := deriveSessionKeys(shared, salt, "server")
	if err != nil {
		return nil, "", err
	}

	sc, err := newSecureConn(conn, writeKey, readKey)
	if err != nil {
		return nil, "", err
	}
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
	return sc, hello.PeerID, nil
}

func sendError(conn net.Conn, peerID, reason string) {
	resp := &helloMsg{
		Type:    "hello_resp",
		Version: HandshakeVersion,
		Status:  "error",
		Reason:  reason,
		PeerID:  peerID,
	}
	_ = json.NewEncoder(conn).Encode(resp)
}

func deriveSessionKeys(shared, salt []byte, role string) (writeKey, readKey []byte, err error) {
	hk := hkdf.New(sha256.New, shared, salt, []byte(sessionInfo))
	keys := make([]byte, 64)
	if _, err := io.ReadFull(hk, keys); err != nil {
		return nil, nil, err
	}
	c2s := keys[:32]
	s2c := keys[32:]
	
	if role == "client" {
		return c2s, s2c, nil
	}
	return s2c, c2s, nil
}