package wgconfig

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

type ClientConfig struct {
	PrivateKey string `json:"private_key"`
	Address    string `json:"address"`
	DNS        string `json:"dns,omitempty"`
	MTU        int    `json:"mtu"`

	Jc   int    `json:"jc"`
	Jmin int    `json:"jmin"`
	Jmax int    `json:"jmax"`
	S1   int    `json:"s1"`
	S2   int    `json:"s2"`
	S3   int    `json:"s3"`
	S4   int    `json:"s4"`
	H1   string `json:"h1,omitempty"`
	H2   string `json:"h2,omitempty"`
	H3   string `json:"h3,omitempty"`
	H4   string `json:"h4,omitempty"`
	I1   string `json:"i1,omitempty"`

	ServerPublicKey     string `json:"server_public_key"`
	Endpoint            string `json:"endpoint"`
	AllowedIPs          string `json:"allowed_ips,omitempty"`
	PersistentKeepalive int    `json:"persistent_keepalive"`

	PeerName string `json:"peer_name,omitempty"`

	Raw string `json:"-"`
}

func Parse(data string) (*ClientConfig, error) {
	cfg := &ClientConfig{MTU: 1280, PersistentKeepalive: 25, Raw: data}
	section := ""

	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch section {
		case "interface":
			switch key {
			case "PrivateKey":
				cfg.PrivateKey = val
			case "Address":
				cfg.Address = val
			case "DNS":
				cfg.DNS = val
			case "MTU":
				cfg.MTU, _ = strconv.Atoi(val)
			case "Jc":
				cfg.Jc, _ = strconv.Atoi(val)
			case "Jmin":
				cfg.Jmin, _ = strconv.Atoi(val)
			case "Jmax":
				cfg.Jmax, _ = strconv.Atoi(val)
			case "S1":
				cfg.S1, _ = strconv.Atoi(val)
			case "S2":
				cfg.S2, _ = strconv.Atoi(val)
			case "S3":
				cfg.S3, _ = strconv.Atoi(val)
			case "S4":
				cfg.S4, _ = strconv.Atoi(val)
			case "H1":
				cfg.H1 = val
			case "H2":
				cfg.H2 = val
			case "H3":
				cfg.H3 = val
			case "H4":
				cfg.H4 = val
			case "I1":
				cfg.I1 = val
			}
		case "peer":
			switch key {
			case "PublicKey":
				cfg.ServerPublicKey = val
			case "Endpoint":
				cfg.Endpoint = val
			case "AllowedIPs":
				cfg.AllowedIPs = val
			case "PersistentKeepalive":
				cfg.PersistentKeepalive, _ = strconv.Atoi(val)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	if cfg.PrivateKey == "" {
		return nil, fmt.Errorf("PrivateKey not found")
	}
	if cfg.ServerPublicKey == "" {
		return nil, fmt.Errorf("server PublicKey not found")
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("Endpoint not found")
	}
	return cfg, nil
}

func (c *ClientConfig) ClientIP() string {
	return strings.TrimSpace(strings.Split(c.Address, "/")[0])
}

func (c *ClientConfig) Subnet24() string {
	parts := strings.Split(c.ClientIP(), ".")
	if len(parts) != 4 {
		return "10.10.0.0/24"
	}
	return fmt.Sprintf("%s.%s.%s.0/24", parts[0], parts[1], parts[2])
}

func (c *ClientConfig) SubnetPrefix() string {
	parts := strings.Split(c.ClientIP(), ".")
	if len(parts) != 4 {
		return "10.10.0."
	}
	return fmt.Sprintf("%s.%s.%s.", parts[0], parts[1], parts[2])
}

func (c *ClientConfig) UAPI(privHex, serverPubHex string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "private_key=%s\n", privHex)
	fmt.Fprintf(&b, "jc=%d\n", c.Jc)
	fmt.Fprintf(&b, "jmin=%d\n", c.Jmin)
	fmt.Fprintf(&b, "jmax=%d\n", c.Jmax)
	fmt.Fprintf(&b, "s1=%d\n", c.S1)
	fmt.Fprintf(&b, "s2=%d\n", c.S2)
	fmt.Fprintf(&b, "s3=%d\n", c.S3)
	fmt.Fprintf(&b, "s4=%d\n", c.S4)
	if c.H1 != "" {
		fmt.Fprintf(&b, "h1=%s\n", c.H1)
	}
	if c.H2 != "" {
		fmt.Fprintf(&b, "h2=%s\n", c.H2)
	}
	if c.H3 != "" {
		fmt.Fprintf(&b, "h3=%s\n", c.H3)
	}
	if c.H4 != "" {
		fmt.Fprintf(&b, "h4=%s\n", c.H4)
	}
	if c.I1 != "" {
		fmt.Fprintf(&b, "i1=%s\n", c.I1)
	}

	fmt.Fprintf(&b, "public_key=%s\n", serverPubHex)
	fmt.Fprintf(&b, "endpoint=%s\n", c.Endpoint)
	fmt.Fprintf(&b, "allowed_ip=%s\n", c.Subnet24())
	fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", c.PersistentKeepalive)

	return b.String()
}