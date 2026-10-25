package vpn

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun"

	"peerdrop-awg/internal/wgconfig"
)

type Client struct {
	TunName string
	Cfg     *wgconfig.ClientConfig
	LogFn   func(string)
	dev     *device.Device
	tun     tun.Device
}

func (c *Client) logf(format string, args ...interface{}) {
	if c.LogFn != nil {
		c.LogFn(fmt.Sprintf(format, args...))
	}
}

func (c *Client) Start() error {
	privHex, err := b64ToHex(c.Cfg.PrivateKey)
	if err != nil {
		return fmt.Errorf("private key: %w", err)
	}
	pubHex, err := b64ToHex(c.Cfg.ServerPublicKey)
	if err != nil {
		return fmt.Errorf("server public key: %w", err)
	}

	mtu := c.Cfg.MTU
	if mtu == 0 {
		mtu = 1280
	}

	tunDev, err := tun.CreateTUN(c.TunName, mtu)
	if err != nil {
		return fmt.Errorf("create TUN (run as administrator, wintun.dll must be next to .exe): %w", err)
	}
	c.tun = tunDev

	realName, err := tunDev.Name()
	if err != nil {
		return fmt.Errorf("tun name: %w", err)
	}
	c.logf("TUN created: %q", realName)

	logger := device.NewLogger(device.LogLevelError, "[awg] ")
	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), logger)
	c.dev = dev

	uapi := c.Cfg.UAPI(privHex, pubHex)
	if err := dev.IpcSet(uapi); err != nil {
		dev.Close()
		_ = tunDev.Close()
		return fmt.Errorf("IpcSet: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		_ = tunDev.Close()
		return fmt.Errorf("device up: %w", err)
	}

	// Исправленная строка здесь:
	if err := configureInterface(realName, c.Cfg.ClientIP(), mtu, func(s string) { c.logf("%s", s) }); err != nil {
		dev.Close()
		_ = tunDev.Close()
		return fmt.Errorf("configure: %w", err)
	}

	if err := waitForIPBindable(c.Cfg.ClientIP(), 20*time.Second); err != nil {
		dev.Close()
		_ = tunDev.Close()
		return fmt.Errorf("wait for IP: %w", err)
	}

	c.logf("Interface %q ready: %s", realName, c.Cfg.ClientIP())
	return nil
}

func (c *Client) Stop() error {
	if c.dev != nil {
		c.dev.Close()
	}
	if c.tun != nil {
		return c.tun.Close()
	}
	return nil
}

func waitForIPBindable(ip string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort(ip, "0")
	var lastErr error
	attempts := 0
	for time.Now().Before(deadline) {
		attempts++
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			_ = ln.Close()
			return nil
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("IP %s not bindable after %s (attempts=%d): %v", ip, timeout, attempts, lastErr)
}

func configureInterface(name, ip string, mtu int, logFn func(string)) error {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("netsh", "interface", "ipv4", "set", "address",
			"name="+name, "static", ip, "255.255.255.0").CombinedOutput()
		if err != nil {
			out2, err2 := exec.Command("netsh", "interface", "ip", "set", "address",
				"name="+name, "static", ip, "255.255.255.0").CombinedOutput()
			if err2 != nil {
				return fmt.Errorf("netsh set address failed.\n  ipv4: %v: %s\n  legacy: %v: %s",
					err, strings.TrimSpace(string(out)),
					err2, strings.TrimSpace(string(out2)))
			}
		}

		deadline := time.Now().Add(10 * time.Second)
		found := false
		for time.Now().Before(deadline) {
			addrs, err := net.InterfaceAddrs()
			if err == nil {
				for _, addr := range addrs {
					if ipnet, ok := addr.(*net.IPNet); ok {
						if ipnet.IP.String() == ip {
							found = true
							break
						}
					}
				}
			}
			if found {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}

		if !found {
			showOut, _ := exec.Command("netsh", "interface", "ipv4", "show", "addresses",
				"name="+name).CombinedOutput()
			return fmt.Errorf("netsh completed, but IP %s was not applied to %q.\nOutput:\n%s",
				ip, name, strings.TrimSpace(string(showOut)))
		}

		if logFn != nil {
			logFn(fmt.Sprintf("netsh: IP %s set on %q", ip, name))
		}

		if mtu > 0 {
			_ = exec.Command("netsh", "interface", "ipv4", "set", "subinterface",
				name, fmt.Sprintf("mtu=%d", mtu), "store=persistent").Run()
		}
	case "linux":
		if out, err := exec.Command("ip", "addr", "add", ip+"/24", "dev", name).CombinedOutput(); err != nil {
			return fmt.Errorf("ip addr: %v: %s", err, out)
		}
		if out, err := exec.Command("ip", "link", "set", name, "up").CombinedOutput(); err != nil {
			return fmt.Errorf("ip link up: %v: %s", err, out)
		}
	case "darwin":
		if out, err := exec.Command("ifconfig", name, "inet", ip, ip, "up").CombinedOutput(); err != nil {
			return fmt.Errorf("ifconfig: %v: %s", err, out)
		}
		_ = exec.Command("route", "-n", "add", "-net", "10.10.0.0/24", "-interface", name).Run()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	return nil
}

func b64ToHex(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}