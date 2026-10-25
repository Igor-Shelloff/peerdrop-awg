package e2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type KnownPeer struct {
	SignPub   []byte
	Name      string
	FirstSeen time.Time
	LastSeen  time.Time
}

type knownPeerJSON struct {
	SignPub   string    `json:"sign_pub"`
	Name      string    `json:"name"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

type KnownPeers struct {
	mu    sync.Mutex
	path  string
	peers map[string]*KnownPeer
}

func LoadKnownPeers(path string) (*KnownPeers, error) {
	kp := &KnownPeers{
		path:  path,
		peers: make(map[string]*KnownPeer),
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return kp, nil
		}
		return nil, err
	}
	var raw map[string]knownPeerJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse known_peers: %w", err)
	}
	for id, r := range raw {
		pub, err := base64.StdEncoding.DecodeString(r.SignPub)
		if err != nil {
			continue
		}
		kp.peers[id] = &KnownPeer{
			SignPub:   pub,
			Name:      r.Name,
			FirstSeen: r.FirstSeen,
			LastSeen:  r.LastSeen,
		}
	}
	return kp, nil
}

func (kp *KnownPeers) Get(id string) (KnownPeer, bool) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	p, ok := kp.peers[id]
	if !ok {
		return KnownPeer{}, false
	}
	return *p, true
}

func (kp *KnownPeers) Update(id, name string, signPub []byte) (firstContact, keyChanged bool, err error) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	now := time.Now()
	if existing, ok := kp.peers[id]; ok {
		if !PubEquals(existing.SignPub, signPub) {
			return false, true, nil
		}
		existing.LastSeen = now
		if name != "" {
			existing.Name = name
		}
		return false, false, kp.saveLocked()
	}
	kp.peers[id] = &KnownPeer{
		SignPub:   append([]byte(nil), signPub...),
		Name:      name,
		FirstSeen: now,
		LastSeen:  now,
	}
	return true, false, kp.saveLocked()
}

func (kp *KnownPeers) Verify(id string, signPub []byte) (known bool, matches bool) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	existing, ok := kp.peers[id]
	if !ok {
		return false, false
	}
	return true, PubEquals(existing.SignPub, signPub)
}

func (kp *KnownPeers) Delete(id string) error {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	delete(kp.peers, id)
	return kp.saveLocked()
}

func (kp *KnownPeers) saveLocked() error {
	raw := make(map[string]knownPeerJSON, len(kp.peers))
	for id, p := range kp.peers {
		raw[id] = knownPeerJSON{
			SignPub:   base64.StdEncoding.EncodeToString(p.SignPub),
			Name:      p.Name,
			FirstSeen: p.FirstSeen,
			LastSeen:  p.LastSeen,
		}
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(kp.path), 0o755); err != nil {
		return err
	}
	tmp := kp.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, kp.path)
}