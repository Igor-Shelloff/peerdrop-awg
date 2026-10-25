package transfer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"peerdrop-awg/internal/e2e"
)

type Server struct {
	listener   net.Listener
	port       int
	log        func(string)
	onAsk      func(Header) bool
	onFile     func(Header, string)
	onText     func(Header)
	saveDir    string
	keys       *e2e.KeyPair
	knownPeers *e2e.KnownPeers
	myID       string
}

func NewServer(
	bindIP string,
	myID string,
	keys *e2e.KeyPair,
	knownPeers *e2e.KnownPeers,
	logFn func(string),
	ask func(Header) bool,
	onFile func(Header, string),
	onText func(Header),
) (*Server, error) {
	if bindIP == "" {
		return nil, fmt.Errorf("bindIP is required (VPN interface IP)")
	}
	addr := net.JoinHostPort(bindIP, "0")
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on VPN IP %s: %w", bindIP, err)
	}

	home, _ := os.UserHomeDir()
	saveDir := filepath.Join(home, "Downloads", "PeerDrop")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("mkdir %s: %w", saveDir, err)
	}

	s := &Server{
		listener:   ln,
		port:       ln.Addr().(*net.TCPAddr).Port,
		log:        logFn,
		onAsk:      ask,
		onFile:     onFile,
		onText:     onText,
		saveDir:    saveDir,
		keys:       keys,
		knownPeers: knownPeers,
		myID:       myID,
	}
	go s.acceptLoop()
	return s, nil
}

func (s *Server) Port() int    { return s.port }
func (s *Server) Close() error { return s.listener.Close() }

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()

	secure, peerID, err := e2e.ServerHandshake(conn, s.keys, s.knownPeers, s.myID)
	if err != nil {
		s.log(fmt.Sprintf("E2E handshake failed from %s: %v", remote, err))
		return
	}
	defer secure.Close()

	r := bufio.NewReader(secure)
	line, err := r.ReadBytes('\n')
	if err != nil {
		s.log("Read header error from " + peerID + ": " + err.Error())
		return
	}
	var hdr Header
	if err := json.Unmarshal(line, &hdr); err != nil {
		s.log("Invalid header from " + peerID + ": " + err.Error())
		return
	}

	if hdr.Size < 0 {
		s.log("Invalid size from " + peerID)
		return
	}

	if hdr.Type == "text" {
		doneResp, _ := json.Marshal(Response{Status: "done"})
		_, _ = secure.Write(append(doneResp, '\n'))
		s.log(fmt.Sprintf("Secret \"%s\" received from %s [E2E]", hdr.Name, peerID))
		if s.onText != nil {
			s.onText(hdr)
		}
		return
	}

	if s.onAsk == nil || !s.onAsk(hdr) {
		resp, _ := json.Marshal(Response{Status: "reject", Reason: "rejected by user"})
		_, _ = secure.Write(append(resp, '\n'))
		s.log("File transfer rejected from " + peerID)
		return
	}

	resp, _ := json.Marshal(Response{Status: "accept"})
	if _, err := secure.Write(append(resp, '\n')); err != nil {
		s.log("Send accept error: " + err.Error())
		return
	}

	safeName := sanitizeName(hdr.Name)
	if safeName == "" {
		safeName = "received.bin"
	}
	out, outPath, err := createUnique(s.saveDir, safeName)
	if err != nil {
		s.log("Create file error: " + err.Error())
		errResp, _ := json.Marshal(Response{Status: "error", Reason: err.Error()})
		_, _ = secure.Write(append(errResp, '\n'))
		return
	}
	defer out.Close()

	n, err := io.CopyN(out, r, hdr.Size)
	if err != nil {
		s.log(fmt.Sprintf("Receive error: %v (got %d/%d)", err, n, hdr.Size))
		errResp, _ := json.Marshal(Response{Status: "error", Reason: err.Error()})
		_, _ = secure.Write(append(errResp, '\n'))
		return
	}

	s.log(fmt.Sprintf("File saved: %s (%d bytes) [E2E]", outPath, n))
	doneResp, _ := json.Marshal(Response{Status: "done", Path: outPath})
	_, _ = secure.Write(append(doneResp, '\n'))

	if s.onFile != nil {
		s.onFile(hdr, outPath)
	}
}

func sanitizeName(name string) string {
	name = filepath.Base(name)
	name = filepath.Clean(name)
	if name == "." || name == ".." || name == "/" || name == "" {
		return ""
	}
	return name
}

func createUnique(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 10000; i++ {
		var candidate string
		if i == 0 {
			candidate = filepath.Join(dir, name)
		} else {
			candidate = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		}
		f, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, candidate, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not find unique name for %q", name)
}