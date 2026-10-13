package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"

	"peerdrop-awg/internal/peers"
	"peerdrop-awg/internal/ssh"
	"peerdrop-awg/internal/token"
	"peerdrop-awg/internal/transfer"
	"peerdrop-awg/internal/vpn"
	"peerdrop-awg/internal/wgconfig"
)

const maxLogLines = 500

const (
	roleHost   = "host"
	roleClient = "client"
)

type App struct {
	fyneApp fyne.App
	window  fyne.Window

	roleSelect     *widget.Select
	vpnStatusLabel *widget.Label
	connectBtn     *widget.Button
	disconnectBtn  *widget.Button
	statusLabel    *widget.Label

	role        string
	roleHolder  *fyne.Container
	hostPanel   *fyne.Container
	clientPanel *fyne.Container

	hostEntry     *widget.Entry
	portEntry     *widget.Entry
	userEntry     *widget.Entry
	passwordEntry *widget.Entry
	ownNameEntry  *widget.Entry
	deployBtn     *widget.Button

	newClientNameEntry *widget.Entry
	newClientPINEntry  *widget.Entry
	createClientBtn    *widget.Button
	tokenOutputEntry   *widget.Entry
	copyTokenBtn       *widget.Button
	saveTokenBtn       *widget.Button

	tokenInput    *widget.Entry
	tokenPINEntry *widget.Entry
	applyTokenBtn *widget.Button

	peersList   *widget.List
	peerItems   []peers.Peer
	refreshBtn  *widget.Button
	myInfoLabel *widget.Label

	peerSelect  *widget.Select
	peerByLabel map[string]peers.Peer

	filePathEntry *widget.Entry
	chooseBtn     *widget.Button
	sendFileBtn   *widget.Button
	progressBar   *widget.ProgressBar
	progressLabel *widget.Label

	secretNameEntry *widget.Entry
	secretContent   *widget.Entry
	secretAllowSave *widget.Check
	sendSecretBtn   *widget.Button

	logEntry     *widget.Entry
	logScroll    *container.Scroll
	logMu        sync.Mutex
	logLines     []string
	logScheduled bool

	vpnClient  *vpn.Client
	clientConf *wgconfig.ClientConfig
	discovery  *peers.Discovery
	server     *transfer.Server
	myID       string
	myName     string
}

func NewApp() *App {
	a := &App{fyneApp: app.NewWithID("com.peerdrop.awg")}
	a.window = a.fyneApp.NewWindow("PeerDrop AWG")
	a.window.Resize(fyne.NewSize(900, 700))
	a.window.SetMaster()

	a.myID = loadOrCreateID()
	a.role = loadOrDetectRole()

	a.buildUI()

	if a.tryLoadSavedConfig() {
		if a.clientConf.PeerName != "" {
			a.myName = a.clientConf.PeerName
		} else {
			a.myName = "PeerDrop-" + shortID(a.myID)
		}
		a.connectBtn.Enable()
		a.setVPNStatus(false)
		a.setStatus("Ready to connect")
		a.log("Saved config found (IP " + a.clientConf.ClientIP() + ", name " + a.myName + "). Click Connect.")
	} else {
		if a.myName == "" {
			a.myName = "PeerDrop-" + shortID(a.myID)
		}
		a.setVPNStatus(false)
		a.setStatus("Ready")
	}
	return a
}

func rolePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "peerdrop-role.txt"
	}
	return filepath.Join(dir, "PeerDrop", "role.txt")
}

func loadOrDetectRole() string {
	if data, err := os.ReadFile(rolePath()); err == nil {
		v := strings.TrimSpace(string(data))
		if v == roleHost || v == roleClient {
			return v
		}
	}
	return roleHost
}

func saveRole(r string) {
	_ = os.MkdirAll(filepath.Dir(rolePath()), 0o755)
	_ = os.WriteFile(rolePath(), []byte(r), 0o644)
}

func loadOrCreateID() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return uuid.NewString()
	}
	cfgDir := filepath.Join(dir, "PeerDrop")
	_ = os.MkdirAll(cfgDir, 0o755)
	idPath := filepath.Join(cfgDir, "id.txt")
	if data, err := os.ReadFile(idPath); err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data))
	}
	id := uuid.NewString()
	_ = os.WriteFile(idPath, []byte(id), 0o644)
	return id
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

func clientConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "peerdrop-client.json"
	}
	return filepath.Join(dir, "PeerDrop", "client.json")
}

func (a *App) saveClientConfig() error {
	if a.clientConf == nil {
		return fmt.Errorf("nothing to save")
	}
	p := clientConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a.clientConf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

func (a *App) tryLoadSavedConfig() bool {
	data, err := os.ReadFile(clientConfigPath())
	if err != nil {
		return false
	}
	var cfg wgconfig.ClientConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}
	if cfg.PrivateKey == "" || cfg.ServerPublicKey == "" || cfg.Endpoint == "" {
		return false
	}
	a.clientConf = &cfg
	return true
}

func (a *App) buildUI() {
	a.logEntry = widget.NewMultiLineEntry()
	a.logEntry.SetPlaceHolder("Events…")
	a.logEntry.Wrapping = fyne.TextWrapWord

	a.statusLabel = widget.NewLabel("Ready")
	a.vpnStatusLabel = widget.NewLabelWithStyle("● Disconnected",
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	a.roleSelect = widget.NewSelect(
		[]string{"Host (I own a VPS)", "Client (I have a token)"},
		func(v string) {
			if strings.HasPrefix(v, "Host") {
				a.switchRole(roleHost)
			} else {
				a.switchRole(roleClient)
			}
		})
	if a.role == roleHost {
		a.roleSelect.SetSelectedIndex(0)
	} else {
		a.roleSelect.SetSelectedIndex(1)
	}

	a.connectBtn = widget.NewButtonWithIcon("Connect", theme.ComputerIcon(),
		func() { go a.connectVPN() })
	a.connectBtn.Disable()
	a.connectBtn.Importance = widget.HighImportance

	a.disconnectBtn = widget.NewButtonWithIcon("Disconnect", theme.CancelIcon(),
		func() { go a.disconnectVPN() })
	a.disconnectBtn.Disable()

	topBar := container.NewBorder(
		nil, nil,
		container.NewHBox(
			widget.NewLabelWithStyle("Role:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			a.roleSelect,
			widget.NewSeparator(),
			a.vpnStatusLabel,
		),
		container.NewHBox(a.connectBtn, a.disconnectBtn),
	)

	a.hostPanel = a.buildHostPanel()
	a.clientPanel = a.buildClientPanel()
	a.roleHolder = container.NewStack(a.hostPanel)
	if a.role == roleClient {
		a.roleHolder.Objects = []fyne.CanvasObject{a.clientPanel}
	}

	setupTab := container.NewScroll(container.NewVBox(
		widget.NewLabelWithStyle("Setup", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Server credentials and peer invitations."),
		widget.NewSeparator(),
		a.roleHolder,
	))

	peersTab := a.buildPeersTab()
	sendTab := a.buildSendTab()
	logTab := a.buildLogTab()

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Setup", theme.SettingsIcon(), setupTab),
		container.NewTabItemWithIcon("Peers", theme.AccountIcon(), peersTab),
		container.NewTabItemWithIcon("Send", theme.MailSendIcon(), sendTab),
		container.NewTabItemWithIcon("Log", theme.DocumentIcon(), logTab),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	content := container.NewBorder(
		container.NewVBox(topBar, widget.NewSeparator()),
		container.NewVBox(widget.NewSeparator(), a.statusLabel),
		nil, nil,
		tabs,
	)
	a.window.SetContent(content)

	a.window.SetOnClosed(func() {
		a.stopServices()
		if a.vpnClient != nil {
			_ = a.vpnClient.Stop()
		}
	})

	a.log("PeerDrop AWG started. Role: " + a.role)
}

func (a *App) buildHostPanel() *fyne.Container {
	a.hostEntry = widget.NewEntry()
	a.hostEntry.SetPlaceHolder("VPS IP address")
	a.portEntry = widget.NewEntry()
	a.portEntry.SetText("22")
	a.userEntry = widget.NewEntry()
	a.userEntry.SetText("root")
	a.passwordEntry = widget.NewPasswordEntry()
	a.passwordEntry.SetPlaceHolder("SSH password")
	a.ownNameEntry = widget.NewEntry()
	a.ownNameEntry.SetText("Host")
	a.ownNameEntry.SetPlaceHolder("Your display name")

	a.deployBtn = widget.NewButtonWithIcon("Deploy server / read my config",
		theme.UploadIcon(), func() { go a.deployServer() })

	sshRow := container.NewGridWithColumns(4,
		a.hostEntry, a.portEntry, a.userEntry, a.passwordEntry,
	)

	serverCard := widget.NewCard("VPS Server",
		"SSH access. Clients do not need this password — they get a token.",
		container.NewVBox(
			sshRow,
			container.NewBorder(nil, nil,
				widget.NewLabel("Display name:"), nil,
				a.ownNameEntry),
			container.NewHBox(layout.NewSpacer(), a.deployBtn),
		))

	a.newClientNameEntry = widget.NewEntry()
	a.newClientNameEntry.SetPlaceHolder("Friend's name (Alice, Bob…)")
	a.newClientPINEntry = widget.NewPasswordEntry()
	a.newClientPINEntry.SetPlaceHolder("PIN (optional)")

	a.createClientBtn = widget.NewButtonWithIcon("Create client",
		theme.ContentAddIcon(), func() { go a.createClientForFriend() })

	a.tokenOutputEntry = widget.NewMultiLineEntry()
	a.tokenOutputEntry.SetPlaceHolder("Token will appear here — copy it and send to your friend")
	a.tokenOutputEntry.Wrapping = fyne.TextWrapBreak
	a.tokenOutputEntry.SetMinRowsVisible(2)

	a.copyTokenBtn = widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		if a.tokenOutputEntry.Text == "" {
			return
		}
		a.window.Clipboard().SetContent(a.tokenOutputEntry.Text)
		a.log("Token copied to clipboard")
		fyne.Do(func() { dialog.ShowInformation("Copied", "Send the token to your friend.", a.window) })
	})
	a.saveTokenBtn = widget.NewButtonWithIcon("Save to file", theme.DocumentSaveIcon(), func() {
		if a.tokenOutputEntry.Text == "" {
			return
		}
		home, _ := os.UserHomeDir()
		name := a.newClientNameEntry.Text
		if name == "" {
			name = "friend"
		}
		path := filepath.Join(home, "Downloads", "peerdrop-token-"+name+".txt")
		if err := os.WriteFile(path, []byte(a.tokenOutputEntry.Text), 0o600); err != nil {
			fyne.Do(func() { dialog.ShowError(err, a.window) })
			return
		}
		a.log("Token saved: " + path)
		fyne.Do(func() { dialog.ShowInformation("Saved", path, a.window) })
	})

	inviteCard := widget.NewCard("Invite a friend",
		"PIN is optional — when set, the token will be encrypted.",
		container.NewVBox(
			container.NewGridWithColumns(2, a.newClientNameEntry, a.newClientPINEntry),
			container.NewHBox(layout.NewSpacer(), a.createClientBtn),
			a.tokenOutputEntry,
			container.NewHBox(a.copyTokenBtn, a.saveTokenBtn),
		))

	return container.NewVBox(serverCard, inviteCard)
}

func (a *App) buildClientPanel() *fyne.Container {
	a.tokenInput = widget.NewMultiLineEntry()
	a.tokenInput.SetPlaceHolder("Paste the token starting with PDROPv1:")
	a.tokenInput.Wrapping = fyne.TextWrapBreak
	a.tokenInput.SetMinRowsVisible(3)

	a.tokenPINEntry = widget.NewPasswordEntry()
	a.tokenPINEntry.SetPlaceHolder("PIN (if token is encrypted)")

	a.applyTokenBtn = widget.NewButtonWithIcon("Apply and connect",
		theme.ConfirmIcon(), func() { go a.applyToken() })
	a.applyTokenBtn.Importance = widget.HighImportance

	card := widget.NewCard("Invitation token",
		"No server password needed — just paste the token.",
		container.NewVBox(
			a.tokenInput,
			a.tokenPINEntry,
			container.NewHBox(layout.NewSpacer(), a.applyTokenBtn),
		))

	return container.NewVBox(card)
}

func (a *App) buildPeersTab() fyne.CanvasObject {
	a.myInfoLabel = widget.NewLabel("Your name: —")

	a.peersList = widget.NewList(
		func() int { return len(a.peerItems) },
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewIcon(theme.AccountIcon()),
				widget.NewLabelWithStyle("template-name",
					fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				widget.NewLabel("template-ip"),
			)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(a.peerItems) {
				return
			}
			p := a.peerItems[i]
			box := o.(*fyne.Container)
			box.Objects[1].(*widget.Label).SetText(p.Name)
			box.Objects[2].(*widget.Label).SetText(fmt.Sprintf("(%s)", p.IP))
		},
	)

	a.refreshBtn = widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		a.updatePeersUI()
	})
	a.refreshBtn.Importance = widget.LowImportance

	header := container.NewBorder(
		nil, nil,
		a.myInfoLabel,
		container.NewHBox(a.refreshBtn),
	)

	return container.NewBorder(
		container.NewVBox(
			header,
			widget.NewSeparator(),
		),
		nil, nil, nil,
		a.peersList,
	)
}

func (a *App) buildSendTab() fyne.CanvasObject {
	a.peerSelect = widget.NewSelect([]string{}, nil)
	a.peerSelect.PlaceHolder = "— select a recipient —"
	a.peerByLabel = make(map[string]peers.Peer)

	receiverRow := container.NewBorder(
		nil, nil,
		widget.NewLabelWithStyle("To:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		nil,
		a.peerSelect,
	)

	a.filePathEntry = widget.NewEntry()
	a.filePathEntry.SetPlaceHolder("No file selected")
	a.filePathEntry.Disable()

	a.chooseBtn = widget.NewButtonWithIcon("Choose file", theme.FolderOpenIcon(), func() {
		dialog.ShowFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil || r == nil {
				return
			}
			defer r.Close()
			a.filePathEntry.SetText(r.URI().Path())
		}, a.window)
	})

	a.sendFileBtn = widget.NewButtonWithIcon("Send file", theme.MailSendIcon(),
		func() { go a.sendFile() })
	a.sendFileBtn.Importance = widget.HighImportance

	a.progressBar = widget.NewProgressBar()
	a.progressLabel = widget.NewLabel("")

	fileCard := widget.NewCard("File", "Recipient will save it to ~/Downloads/PeerDrop",
		container.NewVBox(
			container.NewBorder(nil, nil, nil, a.chooseBtn, a.filePathEntry),
			container.NewHBox(layout.NewSpacer(), a.sendFileBtn),
			a.progressBar,
			a.progressLabel,
		))

	a.secretNameEntry = widget.NewEntry()
	a.secretNameEntry.SetPlaceHolder("Title (e.g., Wi-Fi password)")
	a.secretNameEntry.SetText("secret")

	a.secretContent = widget.NewMultiLineEntry()
	a.secretContent.SetPlaceHolder("Password, key, or short message…")
	a.secretContent.SetMinRowsVisible(3)

	a.secretAllowSave = widget.NewCheck("Allow recipient to save as .txt", nil)

	a.sendSecretBtn = widget.NewButtonWithIcon("Send secret", theme.MailSendIcon(),
		func() { go a.sendSecret() })
	a.sendSecretBtn.Importance = widget.HighImportance

	secretCard := widget.NewCard("Secret",
		"By default not saved to disk: recipient copies it and closes the window.",
		container.NewVBox(
			a.secretNameEntry,
			a.secretContent,
			a.secretAllowSave,
			container.NewHBox(layout.NewSpacer(), a.sendSecretBtn),
		))

	return container.NewScroll(container.NewVBox(
		receiverRow,
		widget.NewSeparator(),
		fileCard,
		secretCard,
	))
}

func (a *App) buildLogTab() fyne.CanvasObject {
	clearBtn := widget.NewButtonWithIcon("Clear", theme.DeleteIcon(), func() {
		a.logMu.Lock()
		a.logLines = nil
		a.logMu.Unlock()
		a.logEntry.SetText("")
	})
	clearBtn.Importance = widget.LowImportance

	a.logScroll = container.NewScroll(a.logEntry)
	a.logScroll.SetMinSize(fyne.NewSize(860, 400))

	return container.NewBorder(
		container.NewHBox(layout.NewSpacer(), clearBtn),
		nil, nil, nil,
		a.logScroll,
	)
}

func (a *App) switchRole(role string) {
	if a.role == role {
		return
	}
	a.role = role
	saveRole(role)

	if a.vpnClient != nil {
		_ = a.vpnClient.Stop()
		a.vpnClient = nil
	}
	a.stopServices()

	fyne.Do(func() {
		a.connectBtn.Disable()
		a.disconnectBtn.Disable()
	})
	a.setVPNStatus(false)
	a.setStatus("Ready")

	a.roleHolder.Objects = nil
	if role == roleHost {
		a.roleHolder.Add(a.hostPanel)
		a.log("Switched to Host mode")
	} else {
		a.roleHolder.Add(a.clientPanel)
		a.log("Switched to Client mode")
	}
	a.roleHolder.Refresh()
}

func (a *App) log(msg string) {
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05"), msg)

	a.logMu.Lock()
	a.logLines = append(a.logLines, line)
	if len(a.logLines) > maxLogLines {
		a.logLines = a.logLines[len(a.logLines)-maxLogLines:]
	}
	a.logMu.Unlock()

	if a.logScheduled {
		return
	}
	a.logScheduled = true
	time.AfterFunc(100*time.Millisecond, a.flushLog)
}

func (a *App) flushLog() {
	a.logMu.Lock()
	text := strings.Join(a.logLines, "")
	a.logMu.Unlock()

	fyne.Do(func() {
		a.logEntry.SetText(text)
		if a.logScroll != nil {
			a.logScroll.ScrollToBottom()
		}
		a.logMu.Lock()
		a.logScheduled = false
		a.logMu.Unlock()
	})
}

func (a *App) setStatus(s string) { fyne.Do(func() { a.statusLabel.SetText(s) }) }

func (a *App) setVPNStatus(online bool) {
	fyne.Do(func() {
		if online {
			a.vpnStatusLabel.SetText("● Connected")
		} else {
			a.vpnStatusLabel.SetText("● Disconnected")
		}
	})
}

func (a *App) confirmDeployment() bool {
	result := make(chan bool, 1)
	fyne.Do(func() {
		msg := "Deployment takes 5–10 minutes and requires administrator privileges.\n\n" +
			"The server will reboot 2–3 times during the process. This is normal.\n\n" +
			"Do NOT close the application while deployment is running.\n\n" +
			"Continue?"
		dialog.ShowConfirm("Start deployment?", msg, func(ok bool) {
			result <- ok
		}, a.window)
	})
	return <-result
}

func (a *App) deployServer() {
	defer func() {
		if r := recover(); r != nil {
			a.log(fmt.Sprintf("PANIC: %v", r))
		}
	}()

	if !a.confirmDeployment() {
		a.log("Deployment cancelled by user")
		return
	}

	host := a.hostEntry.Text
	port := a.portEntry.Text
	user := a.userEntry.Text
	pass := a.passwordEntry.Text
	myName := strings.TrimSpace(a.ownNameEntry.Text)
	if myName == "" {
		myName = "Host"
	}

	if host == "" || pass == "" {
		fyne.Do(func() { dialog.ShowInformation("Error", "Fill in IP and SSH password", a.window) })
		return
	}

	fyne.Do(func() {
		a.deployBtn.Disable()
		a.connectBtn.Disable()
	})
	defer fyne.Do(func() { a.deployBtn.Enable() })

	a.log("→ SSH " + host + ":" + port)
	d, err := ssh.NewDeployer(host, port, user, pass, a.log)
	if err != nil {
		a.log("SSH: " + err.Error())
		fyne.Do(func() { dialog.ShowError(err, a.window) })
		return
	}
	defer d.Close()

	ctx := context.Background()
	if d.IsInstalled(ctx) {
		a.log("AmneziaWG already deployed")
	} else {
		a.log("→ Installing AmneziaWG 2.0 (this may take 5–10 minutes)…")
		if err := d.InstallAmneziaWG(ctx); err != nil {
			a.log("Error: " + err.Error())
			fyne.Do(func() { dialog.ShowError(err, a.window) })
			return
		}
	}

	a.log("→ Preparing my client (" + myName + ")")
	confText, err := d.EnsureClient(ctx, myName)
	if err != nil {
		a.log("Error: " + err.Error())
		fyne.Do(func() { dialog.ShowError(err, a.window) })
		return
	}
	cfg, err := wgconfig.Parse(confText)
	if err != nil {
		a.log("Parse error: " + err.Error())
		fyne.Do(func() { dialog.ShowError(err, a.window) })
		return
	}
	cfg.PeerName = myName
	a.clientConf = cfg
	a.myName = myName
	_ = a.saveClientConfig()

	a.log(fmt.Sprintf("Done: %s  IP=%s", myName, cfg.ClientIP()))
	a.setStatus("Ready to connect VPN")
	fyne.Do(func() { a.connectBtn.Enable() })
}

func (a *App) createClientForFriend() {
	defer func() {
		if r := recover(); r != nil {
			a.log(fmt.Sprintf("PANIC: %v", r))
		}
	}()

	name := strings.TrimSpace(a.newClientNameEntry.Text)
	pin := a.newClientPINEntry.Text
	if name == "" {
		fyne.Do(func() { dialog.ShowInformation("Error", "Enter a name", a.window) })
		return
	}
	host := a.hostEntry.Text
	port := a.portEntry.Text
	user := a.userEntry.Text
	pass := a.passwordEntry.Text
	if host == "" || pass == "" {
		fyne.Do(func() { dialog.ShowInformation("Error", "Fill in IP and SSH password", a.window) })
		return
	}

	fyne.Do(func() { a.createClientBtn.Disable() })
	defer fyne.Do(func() { a.createClientBtn.Enable() })

	a.log("→ Creating client " + name)
	d, err := ssh.NewDeployer(host, port, user, pass, a.log)
	if err != nil {
		a.log("SSH: " + err.Error())
		fyne.Do(func() { dialog.ShowError(err, a.window) })
		return
	}
	defer d.Close()

	ctx := context.Background()
	confText, err := d.EnsureClient(ctx, name)
	if err != nil {
		a.log("Error: " + err.Error())
		fyne.Do(func() { dialog.ShowError(err, a.window) })
		return
	}
	cfg, err := wgconfig.Parse(confText)
	if err != nil {
		a.log("Parse error: " + err.Error())
		fyne.Do(func() { dialog.ShowError(err, a.window) })
		return
	}
	cfg.PeerName = name

	tok, err := token.Encode(cfg, pin)
	if err != nil {
		a.log("Token: " + err.Error())
		fyne.Do(func() { dialog.ShowError(err, a.window) })
		return
	}

	fyne.Do(func() { a.tokenOutputEntry.SetText(tok) })
	if pin == "" {
		a.log("Token for " + name + " ready (unencrypted)")
	} else {
		a.log("Token for " + name + " ready (encrypted)")
	}
}

func (a *App) applyToken() {
	defer func() {
		if r := recover(); r != nil {
			a.log(fmt.Sprintf("PANIC: %v", r))
		}
	}()

	tok := a.tokenInput.Text
	pin := a.tokenPINEntry.Text

	if strings.TrimSpace(tok) == "" {
		fyne.Do(func() { dialog.ShowInformation("Error", "Paste a token", a.window) })
		return
	}

	fyne.Do(func() { a.applyTokenBtn.Disable() })
	defer fyne.Do(func() { a.applyTokenBtn.Enable() })

	a.log("→ Parsing token…")
	cfg, err := token.Decode(tok, pin)
	if err != nil {
		a.log("Error: " + err.Error())
		fyne.Do(func() { dialog.ShowError(err, a.window) })
		return
	}
	if cfg.PeerName == "" {
		cfg.PeerName = "Client-" + shortID(a.myID)
	}
	a.clientConf = cfg
	a.myName = cfg.PeerName
	_ = a.saveClientConfig()

	a.log(fmt.Sprintf("Token accepted: %s IP=%s", cfg.PeerName, cfg.ClientIP()))
	a.setStatus("Token accepted — connecting VPN")
	fyne.Do(func() { a.connectBtn.Enable() })
	go a.connectVPN()
}

func (a *App) connectVPN() {
	defer func() {
		if r := recover(); r != nil {
			a.log(fmt.Sprintf("PANIC: %v", r))
		}
	}()

	if a.clientConf == nil {
		a.log("Configure server or apply a token first")
		return
	}
	a.stopServices()
	if a.vpnClient != nil {
		_ = a.vpnClient.Stop()
		a.vpnClient = nil
	}

	fyne.Do(func() { a.connectBtn.Disable() })
	a.log("Bringing up tunnel…")

	c := &vpn.Client{TunName: "peerdrop", Cfg: a.clientConf}
	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				errCh <- fmt.Errorf("panic: %v", r)
			}
		}()
		errCh <- c.Start()
	}()

	if err := <-errCh; err != nil {
		a.log("VPN: " + err.Error())
		a.setVPNStatus(false)
		fyne.Do(func() { dialog.ShowError(err, a.window) })
		fyne.Do(func() { a.connectBtn.Enable() })
		return
	}

	a.vpnClient = c
	a.setVPNStatus(true)
	a.setStatus("VPN connected")
	a.log("VPN connected: " + a.clientConf.ClientIP() + " (" + a.myName + ")")

	if err := a.startTransferAndDiscovery(); err != nil {
		a.log("Services: " + err.Error())
	}

	fyne.Do(func() {
		a.connectBtn.Enable()
		a.disconnectBtn.Enable()
	})
}

func (a *App) stopServices() {
	if a.discovery != nil {
		a.discovery.Stop()
		a.discovery = nil
	}
	if a.server != nil {
		_ = a.server.Close()
		a.server = nil
	}
}

func (a *App) disconnectVPN() {
	a.stopServices()
	if a.vpnClient != nil {
		_ = a.vpnClient.Stop()
		a.vpnClient = nil
	}
	a.peerItems = nil
	fyne.Do(func() {
		a.peersList.Refresh()
		a.peerSelect.Options = nil
		a.peerSelect.ClearSelected()
		a.peerSelect.Refresh()
		a.disconnectBtn.Disable()
	})
	a.setVPNStatus(false)
	a.setStatus("VPN disconnected")
}

func (a *App) startTransferAndDiscovery() error {
	srv, err := transfer.NewServer(
		a.log,
		func(hdr transfer.Header) bool { return a.askIncoming(hdr) },
		func(hdr transfer.Header, path string) {
			fyne.Do(func() {
				dialog.ShowInformation("File received",
					fmt.Sprintf("%s saved:\n%s", hdr.Name, path), a.window)
			})
		},
		func(hdr transfer.Header) { a.showSecret(hdr) },
	)
	if err != nil {
		return err
	}
	a.server = srv
	a.log(fmt.Sprintf("TCP receive server on port %d", srv.Port()))

	d, err := peers.NewDiscovery(
		a.myID, a.myName, srv.Port(),
		a.clientConf.SubnetPrefix(),
		func(list []peers.Peer) { a.onPeersUpdate(list) },
	)
	if err != nil {
		_ = srv.Close()
		return err
	}
	a.discovery = d
	a.log("Discovery started (" + a.clientConf.Subnet24() + ")")
	return nil
}

func (a *App) askIncoming(hdr transfer.Header) bool {
	result := make(chan bool, 1)
	fyne.Do(func() {
		msg := fmt.Sprintf("Incoming file:\n%s\nSize: %d bytes\n\nAccept?", hdr.Name, hdr.Size)
		dialog.ShowConfirm("Incoming transfer", msg, func(ok bool) {
			result <- ok
		}, a.window)
	})
	return <-result
}

func (a *App) showSecret(hdr transfer.Header) {
	fyne.Do(func() {
		content := widget.NewMultiLineEntry()
		content.SetText(hdr.Content)
		content.Wrapping = fyne.TextWrapWord
		content.SetMinRowsVisible(3)

		copyBtn := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
			a.window.Clipboard().SetContent(hdr.Content)
			a.log("Secret copied to clipboard")
		})
		copyBtn.Importance = widget.HighImportance
		row := container.NewHBox(copyBtn)

		if hdr.AllowSave {
			saveBtn := widget.NewButtonWithIcon("Save as .txt",
				theme.DocumentSaveIcon(), func() {
					home, _ := os.UserHomeDir()
					dir := filepath.Join(home, "Downloads", "PeerDrop")
					_ = os.MkdirAll(dir, 0o755)
					safeName := hdr.Name
					if safeName == "" {
						safeName = "secret"
					}
					path := filepath.Join(dir, safeName+".txt")
					if err := os.WriteFile(path, []byte(hdr.Content), 0o600); err != nil {
						dialog.ShowError(err, a.window)
						return
					}
					a.log("Secret saved: " + path)
					dialog.ShowInformation("Saved", path, a.window)
				})
			row.Add(saveBtn)
		}

		box := container.NewVBox(
			widget.NewLabelWithStyle("Secret: "+hdr.Name,
				fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			content,
			row,
			widget.NewSeparator(),
			widget.NewLabel("Closing this window erases the secret from memory."),
		)
		d := dialog.NewCustom("Incoming secret", "Close", box, a.window)
		d.Resize(fyne.NewSize(520, 380))
		d.Show()
	})
}

func (a *App) onPeersUpdate(list []peers.Peer) {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	a.peerItems = list
	fyne.Do(func() {
		a.peersList.Refresh()

		labels := make([]string, 0, len(list))
		peerMap := make(map[string]peers.Peer, len(list))
		for _, p := range list {
			label := fmt.Sprintf("%s — %s", p.Name, p.IP)
			labels = append(labels, label)
			peerMap[label] = p
		}
		a.peerByLabel = peerMap
		a.peerSelect.Options = labels
		a.peerSelect.Refresh()

		a.myInfoLabel.SetText(fmt.Sprintf("Your name: %s", a.myName))
	})
}

func (a *App) updatePeersUI() {
	if a.discovery != nil {
		a.onPeersUpdate(a.discovery.List())
	}
}

func (a *App) selectedPeer() (peers.Peer, bool) {
	label := a.peerSelect.Selected
	if label == "" {
		return peers.Peer{}, false
	}
	p, ok := a.peerByLabel[label]
	return p, ok
}

func (a *App) sendFile() {
	p, ok := a.selectedPeer()
	if !ok {
		fyne.Do(func() { dialog.ShowInformation("Error", "Select a recipient", a.window) })
		return
	}
	path := a.filePathEntry.Text
	if path == "" {
		fyne.Do(func() { dialog.ShowInformation("Error", "Choose a file", a.window) })
		return
	}

	fyne.Do(func() {
		a.sendFileBtn.Disable()
		a.progressBar.SetValue(0)
		a.progressLabel.SetText("")
	})
	defer fyne.Do(func() { a.sendFileBtn.Enable() })

	a.log(fmt.Sprintf("Sending %s → %s", filepath.Base(path), p.Name))

	err := transfer.SendFile(p.IP, p.TCPPort, path, func(sent, total int64) {
		if total > 0 {
			v := float64(sent) / float64(total)
			fyne.Do(func() {
				a.progressBar.SetValue(v)
				a.progressLabel.SetText(fmt.Sprintf("%d / %d bytes (%.1f%%)",
					sent, total, v*100))
			})
		}
	})
	if err != nil {
		a.log("Error: " + err.Error())
		fyne.Do(func() { dialog.ShowError(err, a.window) })
		return
	}

	a.log("File sent")
	fyne.Do(func() {
		a.progressBar.SetValue(1)
		a.progressLabel.SetText("Done")
		dialog.ShowInformation("Success", "File sent", a.window)
	})
}

func (a *App) sendSecret() {
	p, ok := a.selectedPeer()
	if !ok {
		fyne.Do(func() { dialog.ShowInformation("Error", "Select a recipient", a.window) })
		return
	}
	name := strings.TrimSpace(a.secretNameEntry.Text)
	content := a.secretContent.Text
	if content == "" {
		fyne.Do(func() { dialog.ShowInformation("Error", "Enter a secret", a.window) })
		return
	}
	if name == "" {
		name = "secret"
	}

	fyne.Do(func() { a.sendSecretBtn.Disable() })
	defer fyne.Do(func() { a.sendSecretBtn.Enable() })

	a.log(fmt.Sprintf("Secret \"%s\" → %s", name, p.Name))
	if err := transfer.SendSecret(p.IP, p.TCPPort, name, content, a.secretAllowSave.Checked); err != nil {
		a.log("Error: " + err.Error())
		fyne.Do(func() { dialog.ShowError(err, a.window) })
		return
	}
	a.log("Secret sent")
	fyne.Do(func() {
		a.secretContent.SetText("")
		dialog.ShowInformation("Success", "Secret sent", a.window)
	})
}

func (a *App) Run() { a.window.ShowAndRun() }

var _ = runtime.GOOS