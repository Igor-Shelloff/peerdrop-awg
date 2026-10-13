package peers

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	BroadcastPort    = 51821
	AnnounceInterval = 3 * time.Second
	PeerTimeout      = 15 * time.Second
)

type Peer struct {
	ID       string
	Name     string
	IP       string
	TCPPort  int
	LastSeen time.Time
}

type discoveryMsg struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	TCPPort int    `json:"tcp_port"`
}

type Discovery struct {
	id           string
	name         string
	tcpPort      int
	subnetPrefix string
	peers        map[string]*Peer
	mu           sync.RWMutex
	onUpdate     func([]Peer)
	stopCh       chan struct{}
	conn         *net.UDPConn
}

func NewDiscovery(id, name string, tcpPort int, subnetPrefix string, onUpdate func([]Peer)) (*Discovery, error) {
	if subnetPrefix == "" {
		subnetPrefix = "10.10.0."
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: BroadcastPort})
	if err != nil {
		return nil, fmt.Errorf("listen udp %d: %w (application already running?)", BroadcastPort, err)
	}

	d := &Discovery{
		id:           id,
		name:         name,
		tcpPort:      tcpPort,
		subnetPrefix: subnetPrefix,
		peers:        make(map[string]*Peer),
		onUpdate:     onUpdate,
		stopCh:       make(chan struct{}),
		conn:         conn,
	}

	go d.listenLoop()
	go d.announceLoop()
	go d.cleanupLoop()
	return d, nil
}

func (d *Discovery) Stop() {
	select {
	case <-d.stopCh:
		return
	default:
		close(d.stopCh)
	}
	_ = d.conn.Close()
}

func (d *Discovery) List() []Peer {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]Peer, 0, len(d.peers))
	for _, p := range d.peers {
		out = append(out, *p)
	}
	return out
}

func (d *Discovery) listenLoop() {
	buf := make([]byte, 4096)
	for {
		select {
		case <-d.stopCh:
			return
		default:
		}
		_ = d.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, addr, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}

		var msg discoveryMsg
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			continue
		}
		if msg.ID == d.id {
			continue
		}

		ip := addr.IP.String()

		d.mu.Lock()
		if p, ok := d.peers[msg.ID]; ok {
			p.LastSeen = time.Now()
			p.IP = ip
			p.TCPPort = msg.TCPPort
			p.Name = msg.Name
		} else {
			d.peers[msg.ID] = &Peer{
				ID:       msg.ID,
				Name:     msg.Name,
				IP:       ip,
				TCPPort:  msg.TCPPort,
				LastSeen: time.Now(),
			}
		}
		d.mu.Unlock()

		if d.onUpdate != nil {
			d.onUpdate(d.List())
		}
	}
}

func (d *Discovery) announceLoop() {
	ticker := time.NewTicker(AnnounceInterval)
	defer ticker.Stop()

	msg := discoveryMsg{ID: d.id, Name: d.name, TCPPort: d.tcpPort}
	data, _ := json.Marshal(msg)
	d.sweep(data)

	for {
		select {
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.sweep(data)
		}
	}
}

func (d *Discovery) sweep(data []byte) {
	_, _ = d.conn.WriteToUDP(data, &net.UDPAddr{
		IP:   net.ParseIP(d.subnetPrefix + "255"),
		Port: BroadcastPort,
	})

	for i := 2; i <= 254; i++ {
		addr := &net.UDPAddr{
			IP:   net.ParseIP(fmt.Sprintf("%s%d", d.subnetPrefix, i)),
			Port: BroadcastPort,
		}
		_, _ = d.conn.WriteToUDP(data, addr)
	}
}

func (d *Discovery) cleanupLoop() {
	ticker := time.NewTicker(PeerTimeout / 2)
	defer ticker.Stop()
	for {
		select {
		case <-d.stopCh:
			return
		case <-ticker.C:
			now := time.Now()
			changed := false
			d.mu.Lock()
			for id, p := range d.peers {
				if now.Sub(p.LastSeen) > PeerTimeout {
					delete(d.peers, id)
					changed = true
				}
			}
			d.mu.Unlock()
			if changed && d.onUpdate != nil {
				d.onUpdate(d.List())
			}
		}
	}
}