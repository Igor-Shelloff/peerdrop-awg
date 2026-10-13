package ssh

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/melbahja/goph/v2"
)

const (
	manageScript = "/root/awg/manage_amneziawg.sh"
	clientDir    = "/root/awg"
	installerURL = "https://raw.githubusercontent.com/bivlked/amneziawg-installer/main/install_amneziawg.sh"
)

type Deployer struct {
	host     string
	port     string
	user     string
	password string
	client   *goph.Client
	log      func(string)
}

func NewDeployer(host, port, user, password string, logFn func(string)) (*Deployer, error) {
	d := &Deployer{
		host:     host,
		port:     port,
		user:     user,
		password: password,
		log:      logFn,
	}
	if err := d.connect(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Deployer) connect() error {
	if d.port == "" {
		d.port = "22"
	}
	addr := d.host
	if d.port != "22" {
		addr = fmt.Sprintf("%s:%s", d.host, d.port)
	}
	c, err := goph.New(d.user, addr,
		goph.WithPassword(d.password),
		goph.WithInsecureIgnoreHostKey(),
	)
	if err != nil {
		return fmt.Errorf("SSH %s: %w", addr, err)
	}
	d.client = c
	d.log("SSH connected: " + addr)
	return nil
}

func (d *Deployer) hardReconnect() {
	if d.client != nil {
		_ = d.client.Close()
	}
	d.client = nil
}

func (d *Deployer) waitUntilReady(ctx context.Context, timeout time.Duration) error {
	d.hardReconnect()
	d.log(fmt.Sprintf("Waiting for server reboot (up to %s)…", timeout))

	deadline := time.Now().Add(timeout)
	attempt := 0
	lastLog := time.Now()

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}

		attempt++
		if err := d.connect(); err != nil {
			if time.Since(lastLog) > 30*time.Second {
				d.log(fmt.Sprintf("…still waiting (attempts: %d)", attempt))
				lastLog = time.Now()
			}
			continue
		}
		out, err := d.rawRun("echo ok", 10*time.Second)
		if err == nil && strings.Contains(out, "ok") {
			d.log(fmt.Sprintf("Server is back (attempts: %d)", attempt))
			return nil
		}
		d.hardReconnect()
	}
	return fmt.Errorf("server did not come back within %s", timeout)
}

func (d *Deployer) runInstaller(ctx context.Context) (bool, error) {
	script := `
set -e
cd /root
if [ ! -f install_amneziawg.sh ]; then
  curl -fsSL -o install_amneziawg.sh ` + installerURL + `
  chmod +x install_amneziawg.sh
fi
sudo ./install_amneziawg.sh --yes --protocol=2.0 --isolation=off < /dev/null 2>&1 | tee -a /root/awg_deploy.log
`
	d.log("Running installer (--isolation=off, clients will not be isolated)…")
	out, err := d.rawRun(script, 25*time.Minute)
	if err != nil {
		if looksLikeConnectionLoss(err) {
			d.log("Connection dropped — server is likely rebooting")
			return false, nil
		}
		return false, fmt.Errorf("installer failed: %w\n--- output ---\n%s", err, out)
	}
	return true, nil
}

func (d *Deployer) InstallAmneziaWG(ctx context.Context) error {
	if _, err := d.runInstaller(ctx); err != nil {
		return err
	}

	deadline := time.Now().Add(30 * time.Minute)
	iteration := 0

	for time.Now().Before(deadline) {
		iteration++
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.client == nil {
			if err := d.waitUntilReady(ctx, 5*time.Minute); err != nil {
				return fmt.Errorf("waiting for server: %w", err)
			}
			time.Sleep(10 * time.Second)
		}

		if d.isServiceActive(ctx) {
			d.log("Service awg-quick@awg0 is active, checking stability…")
			time.Sleep(10 * time.Second)
			if d.isServiceActive(ctx) {
				if err := d.ensureClientForwarding(ctx); err != nil {
					d.log("Warning (forwarding): " + err.Error())
				}
				d.log("AmneziaWG 2.0 installed and running")
				return nil
			}
			d.log("Service stopped being active, continuing…")
			continue
		}

		state, _ := d.rawRun("cat /root/awg/setup_state 2>/dev/null || echo missing", 10*time.Second)
		state = strings.TrimSpace(state)
		d.log(fmt.Sprintf("Service inactive (state=%s, iteration %d)", state, iteration))

		if d.client != nil {
			d.log("Re-running installer to continue…")
			if _, err := d.runInstaller(ctx); err != nil {
				return err
			}
		}

		time.Sleep(10 * time.Second)
	}

	return fmt.Errorf("installation did not complete within 30 minutes")
}

func (d *Deployer) isServiceActive(ctx context.Context) bool {
	if d.client == nil {
		return false
	}
	out, err := d.rawRun("systemctl is-active awg-quick@awg0 2>/dev/null || echo inactive", 10*time.Second)
	if err != nil {
		if looksLikeConnectionLoss(err) {
			d.log("SSH lost during service check, reconnecting…")
			d.hardReconnect()
		}
		return false
	}
	return strings.TrimSpace(out) == "active"
}

func (d *Deployer) ensureClientForwarding(ctx context.Context) error {
	iface := d.detectAwgInterface(ctx)
	if iface == "" {
		iface = "awg0"
	}

	out, _ := d.rawRun(fmt.Sprintf(
		"iptables -C FORWARD -i %s -o %s -j DROP 2>/dev/null && echo FOUND || echo OK",
		iface, iface), 10*time.Second)
	if strings.Contains(out, "OK") {
		return nil
	}

	d.log("Client isolation detected — removing")

	script := fmt.Sprintf(`
set +e
IFACE=%s

while iptables -C FORWARD -i "$IFACE" -o "$IFACE" -j DROP 2>/dev/null; do
  iptables -D FORWARD -i "$IFACE" -o "$IFACE" -j DROP
done
while iptables -C FORWARD -i "$IFACE" -o "$IFACE" -j REJECT 2>/dev/null; do
  iptables -D FORWARD -i "$IFACE" -o "$IFACE" -j REJECT
done

CIDR=$(grep -E '^Address' /etc/amnezia/amneziawg/%s.conf 2>/dev/null | head -1 | awk '{print $3}')
SUBNET=$(echo "$CIDR" | sed -E 's|\.[0-9]+/[0-9]+$|.0/24|')

if [ -n "$SUBNET" ]; then
  while iptables -C FORWARD -s "$SUBNET" -d "$SUBNET" -j DROP 2>/dev/null; do
    iptables -D FORWARD -s "$SUBNET" -d "$SUBNET" -j DROP
  done
  while iptables -C FORWARD -s "$SUBNET" -d "$SUBNET" -j REJECT 2>/dev/null; do
    iptables -D FORWARD -s "$SUBNET" -d "$SUBNET" -j REJECT
  done
fi

iptables -C FORWARD -i "$IFACE" -o "$IFACE" -j ACCEPT 2>/dev/null || \
  iptables -I FORWARD 1 -i "$IFACE" -o "$IFACE" -j ACCEPT

if [ -n "$SUBNET" ]; then
  iptables -C FORWARD -s "$SUBNET" -d "$SUBNET" -j ACCEPT 2>/dev/null || \
    iptables -I FORWARD 1 -s "$SUBNET" -d "$SUBNET" -j ACCEPT
fi

echo "=== FORWARD rules ==="
iptables -L FORWARD -n --line-numbers | grep -E "(Chain|$IFACE|$SUBNET)" || echo "(no awg rules)"
echo "=== DONE ==="
`, iface, iface)

	out, err := d.rawRun(script, 60*time.Second)
	if err != nil {
		return fmt.Errorf("iptables: %w\n%s", err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			d.log("  " + line)
		}
	}

	_, _ = d.rawRun("netfilter-persistent save 2>/dev/null || true", 15*time.Second)

	if err := d.ensurePostUpRule(ctx, iface); err != nil {
		d.log("Warning: failed to update PostUp: " + err.Error())
	}
	return nil
}

func (d *Deployer) detectAwgInterface(ctx context.Context) string {
	out, err := d.rawRun(
		"ls /etc/amnezia/amneziawg/*.conf 2>/dev/null | head -1 | xargs -n1 basename 2>/dev/null | sed 's/.conf$//'",
		10*time.Second)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (d *Deployer) ensurePostUpRule(ctx context.Context, iface string) error {
	confPath := "/etc/amnezia/amneziawg/" + iface + ".conf"

	out, _ := d.rawRun(
		fmt.Sprintf("grep -q 'FORWARD -i %s -o %s -j ACCEPT' %s && echo FOUND || echo MISSING",
			iface, iface, confPath),
		10*time.Second)
	if strings.Contains(out, "FOUND") {
		return nil
	}

	rule := fmt.Sprintf("iptables -I FORWARD 1 -i %s -o %s -j ACCEPT", iface, iface)

	script := fmt.Sprintf(`
set -e
conf=%s
rule="%s"
cp "$conf" "$conf.bak.$(date +%%s)" 2>/dev/null || true
if grep -q '^PostUp' "$conf"; then
  sed -i "s|^PostUp = \(.*\)$|PostUp = \1; $rule|" "$conf"
else
  sed -i "/^\[Interface\]/a PostUp = $rule" "$conf"
fi
systemctl restart awg-quick@%s
echo UPDATED
`, confPath, rule, iface)

	out, err := d.rawRun(script, 60*time.Second)
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	if strings.Contains(out, "UPDATED") {
		d.log("  PostUp updated")
	}
	return nil
}

func (d *Deployer) Close() error {
	if d.client != nil {
		return d.client.Close()
	}
	return nil
}

func (d *Deployer) IsInstalled(ctx context.Context) bool {
	out, err := d.rawRun("systemctl is-active awg-quick@awg0 2>/dev/null || echo inactive", 15*time.Second)
	if err == nil && strings.TrimSpace(out) == "active" {
		return true
	}
	out, err = d.run(ctx, "test -f "+manageScript+" && echo yes || echo no", 15*time.Second)
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "yes"
}

func (d *Deployer) EnsureClient(ctx context.Context, name string) (string, error) {
	out, err := d.run(ctx,
		fmt.Sprintf("test -f %s/%s.conf && echo yes || echo no", clientDir, name),
		15*time.Second)
	if err == nil && strings.TrimSpace(out) == "yes" {
		d.log("Client " + name + " already exists, reading config")
		return d.run(ctx,
			fmt.Sprintf("sudo cat %s/%s.conf < /dev/null", clientDir, name),
			30*time.Second)
	}

	d.log("Creating client " + name)
	_, err = d.run(ctx,
		fmt.Sprintf("sudo bash %s add %s < /dev/null", manageScript, name),
		2*time.Minute)
	if err != nil {
		return "", fmt.Errorf("add client: %w", err)
	}
	return d.run(ctx,
		fmt.Sprintf("sudo cat %s/%s.conf < /dev/null", clientDir, name),
		30*time.Second)
}

func (d *Deployer) run(ctx context.Context, cmd string, timeout time.Duration) (string, error) {
	out, err := d.rawRun(cmd, timeout)
	if err != nil && looksLikeConnectionLoss(err) {
		d.log("SSH connection lost, reconnecting…")
		if rerr := d.waitUntilReady(ctx, 3*time.Minute); rerr != nil {
			return out, fmt.Errorf("reconnect: %w (initial: %v)", rerr, err)
		}
		return d.rawRun(cmd, timeout)
	}
	return out, err
}

func (d *Deployer) rawRun(cmd string, timeout time.Duration) (string, error) {
	if d.client == nil {
		return "", fmt.Errorf("SSH client not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := d.client.RunContext(ctx, cmd)
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

func looksLikeConnectionLoss(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	markers := []string{
		"broken pipe",
		"connection reset",
		"connection refused",
		"connection closed",
		"connection lost",
		"reset by peer",
		"eof",
		"unexpected eof",
		"no route to host",
		"network is unreachable",
		"i/o timeout",
		"dial tcp",
		"read: connection",
		"write: connection",
		"use of closed network connection",
		"ssh:",
		"session closed",
		"channel closed",
		"context deadline exceeded",
		"exit status 255",
		"wait: remote command",
		"exited without exit status",
		"process exited",
	}
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}