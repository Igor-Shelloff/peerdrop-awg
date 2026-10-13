package vpn

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun"

	"peerdrop-awg/internal/wgconfig"
)

type Client struct {
	TunName string
	Cfg     *wgconfig.ClientConfig
	dev     *device.Device
	tun     tun.Device
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
	log.Printf("TUN created: %q", realName)

	logger := device.NewLogger(device.LogLevelError, "[awg] ")
	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), logger)
	c.dev = dev

	uapi := c.Cfg.UAPI(privHex, pubHex)
	log.Printf("UAPI:\n%s", uapi)

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

	if err := configureInterface(realName, c.Cfg.ClientIP(), mtu); err != nil {
		dev.Close()
		_ = tunDev.Close()
		return fmt.Errorf("configure: %w", err)
	}
	log.Printf("Interface %q: %s", realName, c.Cfg.ClientIP())
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

func configureInterface(name, ip string, mtu int) error {
	switch runtime.GOOS {
	case "windows":
		if out, err := exec.Command("netsh", "interface", "ip", "set", "address",
			"name="+name, "static", ip, "255.255.255.0").CombinedOutput(); err != nil {
			return fmt.Errorf("netsh set address: %v: %s", err, out)
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