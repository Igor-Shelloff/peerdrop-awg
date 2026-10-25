package transfer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"peerdrop-awg/internal/e2e"
)

func dialSecure(
	ip string, port int,
	expectedPeerID string,
	myID string,
	keys *e2e.KeyPair,
	knownPeers *e2e.KnownPeers,
) (*e2e.SecureConn, error) {
	rawConn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to %s:%d: %w", ip, port, err)
	}
	secure, err := e2e.ClientHandshake(rawConn, keys, knownPeers, expectedPeerID, myID)
	if err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("E2E handshake: %w", err)
	}
	return secure, nil
}

func SendFile(
	ip string, port int, expectedPeerID string,
	myID string,
	filePath string,
	keys *e2e.KeyPair, knownPeers *e2e.KnownPeers,
	progress func(int64, int64),
) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}

	secure, err := dialSecure(ip, port, expectedPeerID, myID, keys, knownPeers)
	if err != nil {
		return err
	}
	defer secure.Close()

	hdr := Header{Type: "file", Name: info.Name(), Size: info.Size()}
	hdrBytes, _ := json.Marshal(hdr)
	if _, err := secure.Write(append(hdrBytes, '\n')); err != nil {
		return fmt.Errorf("send header: %w", err)
	}

	r := bufio.NewReader(secure)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if resp.Status != "accept" {
		return fmt.Errorf("recipient rejected: %s", resp.Reason)
	}

	buf := make([]byte, 64*1024)
	var sent int64
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if _, werr := secure.Write(buf[:n]); werr != nil {
				return fmt.Errorf("write: %w", werr)
			}
			sent += int64(n)
			if progress != nil {
				progress(sent, hdr.Size)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
	}

	line, err = r.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read final ack: %w", err)
	}
	var finalResp Response
	if err := json.Unmarshal(line, &finalResp); err != nil {
		return fmt.Errorf("parse final ack: %w", err)
	}
	if finalResp.Status != "done" {
		return fmt.Errorf("recipient reported: %s", finalResp.Reason)
	}
	return nil
}

func SendSecret(
	ip string, port int, expectedPeerID string,
	myID string,
	name, content string, allowSave bool,
	keys *e2e.KeyPair, knownPeers *e2e.KnownPeers,
) error {
	secure, err := dialSecure(ip, port, expectedPeerID, myID, keys, knownPeers)
	if err != nil {
		return err
	}
	defer secure.Close()

	hdr := Header{Type: "text", Name: name, Content: content, AllowSave: allowSave}
	hdrBytes, _ := json.Marshal(hdr)
	if _, err := secure.Write(append(hdrBytes, '\n')); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	r := bufio.NewReader(secure)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if resp.Status != "done" && resp.Status != "accept" {
		return fmt.Errorf("recipient rejected: %s", resp.Reason)
	}
	return nil
}