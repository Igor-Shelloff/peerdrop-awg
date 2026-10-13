package transfer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

func SendFile(ip string, port int, filePath string, progress func(int64, int64)) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect to %s:%d: %w", ip, port, err)
	}
	defer conn.Close()

	hdr := Header{Type: "file", Name: info.Name(), Size: info.Size()}
	hdrBytes, _ := json.Marshal(hdr)
	if _, err := conn.Write(append(hdrBytes, '\n')); err != nil {
		return fmt.Errorf("send header: %w", err)
	}

	r := bufio.NewReader(conn)
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
			if _, werr := conn.Write(buf[:n]); werr != nil {
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
	return nil
}

func SendSecret(ip string, port int, name, content string, allowSave bool) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	hdr := Header{
		Type:      "text",
		Name:      name,
		Content:   content,
		AllowSave: allowSave,
	}
	hdrBytes, _ := json.Marshal(hdr)
	if _, err := conn.Write(append(hdrBytes, '\n')); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	r := bufio.NewReader(conn)
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