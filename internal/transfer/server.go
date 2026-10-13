package transfer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

type Server struct {
	listener net.Listener
	port     int
	log      func(string)
	onAsk    func(Header) bool
	onFile   func(Header, string)
	onText   func(Header)
	saveDir  string
}

func NewServer(
	logFn func(string),
	ask func(Header) bool,
	onFile func(Header, string),
	onText func(Header),
) (*Server, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("listen tcp: %w", err)
	}

	home, _ := os.UserHomeDir()
	saveDir := filepath.Join(home, "Downloads", "PeerDrop")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("mkdir %s: %w", saveDir, err)
	}

	s := &Server{
		listener: ln,
		port:     ln.Addr().(*net.TCPAddr).Port,
		log:      logFn,
		onAsk:    ask,
		onFile:   onFile,
		onText:   onText,
		saveDir:  saveDir,
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

	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		s.log("Read header error from " + remote + ": " + err.Error())
		return
	}

	var hdr Header
	if err := json.Unmarshal(line, &hdr); err != nil {
		s.log("Invalid header from " + remote + ": " + err.Error())
		return
	}

	if hdr.Type == "text" {
		doneResp, _ := json.Marshal(Response{Status: "done"})
		_, _ = conn.Write(append(doneResp, '\n'))

		s.log(fmt.Sprintf("Secret \"%s\" received from %s", hdr.Name, remote))
		if s.onText != nil {
			s.onText(hdr)
		}
		return
	}

	if s.onAsk == nil || !s.onAsk(hdr) {
		resp, _ := json.Marshal(Response{Status: "reject", Reason: "rejected by user"})
		_, _ = conn.Write(append(resp, '\n'))
		s.log("File transfer rejected from " + remote)
		return
	}

	resp, _ := json.Marshal(Response{Status: "accept"})
	if _, err := conn.Write(append(resp, '\n')); err != nil {
		s.log("Send accept error: " + err.Error())
		return
	}

	safeName := sanitizeName(hdr.Name)
	if safeName == "" {
		safeName = "received.bin"
	}
	outPath := filepath.Join(s.saveDir, safeName)
	out, err := os.Create(outPath)
	if err != nil {
		s.log("Create file error: " + err.Error())
		return
	}
	defer out.Close()

	n, err := io.CopyN(out, r, hdr.Size)
	if err != nil {
		s.log(fmt.Sprintf("Receive error: %v (got %d/%d)", err, n, hdr.Size))
		return
	}

	s.log(fmt.Sprintf("File saved: %s (%d bytes)", outPath, n))
	doneResp, _ := json.Marshal(Response{Status: "done", Path: outPath})
	_, _ = conn.Write(append(doneResp, '\n'))

	if s.onFile != nil {
		s.onFile(hdr, outPath)
	}
}

func sanitizeName(name string) string {
	name = filepath.Base(name)
	name = filepath.Clean(name)
	if name == "." || name == ".." || name == "/" {
		return ""
	}
	return name
}