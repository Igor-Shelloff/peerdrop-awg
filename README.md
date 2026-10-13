<div align="center">

# 📦 PeerDrop AWG

**A modern open-source desktop app for peer-to-peer file & secret sharing over an auto-deployed AmneziaWG VPN.**

Transfer files and secrets directly between computers — no cloud, no third-party servers, no NAT issues.

[![Go](https://img.shields.io/badge/Go-1.27%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Fyne](https://img.shields.io/badge/UI-Fyne-1f5580)](https://fyne.io/)
[![AmneziaWG](https://img.shields.io/badge/VPN-AmneziaWG-2CA5E0)](https://amnezia.org/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)]()
[![Version](https://img.shields.io/badge/Version-1.0.0-blue)]()

[Features](#-features) • [Installation](#-installation) • [Usage](#-usage) • [Security](#-security-model) • [FAQ](#-faq) • [Support](#-support-the-project)

</div>

---

## 📖 About

**PeerDrop AWG** is a free, open-source desktop application that lets you transfer files and secrets directly between computers without any cloud services. It works by automatically deploying an [AmneziaWG](https://amnezia.org/) VPN server on your own VPS — after that, all peers join a private virtual network and see each other as if they were on the same LAN.

Perfect for:

- **Large file transfers** — videos, archives, anything — no size limits, no upload throttling
- **Secret sharing** — send passwords, API keys or one-time codes that are not written to disk
- **Cross-network transfers** — between home, office, or different countries, even behind strict NAT
- **DPI bypass** — AmneziaWG obfuscation protects against deep packet inspection

The app talks to your VPS only during the initial deployment. After that, files travel directly between peers over an encrypted VPN tunnel.

---

## ✨ Features

- 🚀 **One-click server deployment** — enter VPS IP + SSH password, the app installs AmneziaWG, reboots if needed, and configures everything automatically
- 🎭 **Host / Client modes** — hosts manage the server and generate invite tokens; clients just paste a token
- 🔐 **Encrypted invite tokens** — share access without revealing server credentials (optional PIN protection)
- 👥 **Peer discovery** — automatic detection of online users in the VPN network
- 📤 **File transfer** — send any file with progress bar and recipient confirmation
- 🔒 **Secret sharing** — send passwords or keys that are **not written to disk** by default; recipient copies and the window closes
- ⚙️ **Optional saving** — sender can allow the recipient to save the secret as a `.txt` file
- 🏷️ **Friendly names** — assign nicknames when creating clients; they appear in the peer list
- 📜 **Built-in event log** — see exactly what's happening during deployment and transfers
- 🆓 **Free & open source** — no license keys, no subscriptions

---

## 📸 Screenshots

<div align="center">

| Setup | Peers |
|:---:|:---:|
| ![Setup](docs/screenshots/setup.png) | ![Peers](docs/screenshots/peers.png) |

| Send | Log |
|:---:|:---:|
| ![Send](docs/screenshots/send.png) | ![Log](docs/screenshots/log.png) |

</div>

---

## 🚀 Installation

### Option 1: Download prebuilt executable (recommended)

Grab the latest release from the [Releases page](https://github.com/Igor-Shelloff/peerdrop-awg/releases/latest):

- **Windows** — `peerdrop-awg-win64.zip` (contains `peerdrop-awg.exe` and `wintun.dll`)

**Setup:**

1. Unzip **both files** (`peerdrop-awg.exe` and `wintun.dll`) into the same folder
2. Right-click `peerdrop-awg.exe` → **Run as administrator**

> ⚠️ **Important:** `wintun.dll` must be in the same folder as the executable. Without it, the app cannot create the VPN network adapter.

> ⚠️ **Windows SmartScreen:** The app is not code-signed. On first launch click **More info** → **Run anyway**. The source code is fully open — you can build it yourself.

### Option 2: Run from source

**Requirements:** Go 1.27 or newer, and `wintun.dll` next to the final binary.

```bash
git clone https://github.com/Igor-Shelloff/peerdrop-awg.git
cd peerdrop-awg
go mod tidy
```

**Build (software renderer, no CGO — works everywhere):**

```powershell
$env:GOOS="windows"
$env:GOARCH="amd64"
$env:CGO_ENABLED="0"
go build -ldflags="-H windowsgui -s -w" -o peerdrop-awg.exe .
```

Place `wintun.dll` next to `peerdrop-awg.exe` and run as administrator.

---

## 📚 Usage

### Host mode (you own a VPS)

1. Launch the app and select **Host** mode in the top-right dropdown
2. Fill in:
   - **VPS IP address** (e.g., `31.31.192.182`)
   - **SSH port** (default `22`)
   - **SSH user** (default `root`)
   - **SSH password**
   - **Your display name** — shown to other peers
3. Click **Deploy server / read my config**
   - The app connects via SSH, installs AmneziaWG 2.0, and configures the server
   - ⏳ This takes **5–10 minutes** and the server may reboot **2–3 times** — this is normal. Do not close the app.
4. Once done, click **Connect** to join the VPN
5. To invite a friend:
   - Enter their **name** and an optional **PIN** (encrypts the token)
   - Click **Create client**
   - Copy the generated **token** and send it to them via any messenger

### Client mode (you have a token)

1. Launch the app and select **Client** mode
2. Paste the **token** you received from the host
3. If the token was encrypted with a PIN, enter it in the **PIN** field
4. Click **Apply and connect**
5. The app automatically connects to the VPN and discovers other peers

### Sending files & secrets

1. Go to the **Peers** tab and make sure the recipient is online
2. Go to the **Send** tab
3. Select the recipient from the **To:** dropdown
4. **File:** choose a file → click **Send file**. Recipient confirms, file saves to `~/Downloads/PeerDrop`
5. **Secret:** enter a title and content → optionally check *Allow recipient to save as .txt* → click **Send secret**. By default the secret appears in a popup on the recipient's side with a **Copy** button; closing the popup removes it from the app's state

---

## 🔐 Security Model

We want to be honest about what PeerDrop AWG protects against and what it doesn't. **Read this before sending anything sensitive.**

### What is protected

- **Traffic between each peer and the VPS** is encrypted and obfuscated by AmneziaWG. An outside observer (ISP, DPI) cannot see file contents or even recognize the traffic as a VPN.
- **Invite tokens** can be optionally encrypted with a PIN (AES-256-GCM + PBKDF2). Without the PIN, a leaked token is useless.
- **Config files** are written with `0600` permissions on Unix-like systems and stored per-user on Windows.
- **Secrets** are not written to disk by default — they are shown once with a Copy button.

### What is **not** protected

- **The VPS sees decrypted traffic between peers.** In the standard hub-and-spoke WireGuard setup, the VPS terminates the VPN tunnels. If peer A sends a file to peer B, the packet passes through the VPS in decrypted form. **Do not use a VPS you don't trust** for sensitive transfers.
- **No end-to-end encryption between peers.** PeerDrop AWG does not add an extra layer of encryption on top of WireGuard. The TCP traffic between peers is only as private as the VPN itself.
- **Read-once secrets are not securely wiped from memory.** Go uses a garbage collector and immutable strings — copies of the secret may remain in RAM until the process exits. When you click **Copy**, the secret also goes into the OS clipboard, which is visible to other applications and may persist until overwritten.
- **Received files can overwrite existing files with the same name** in `~/Downloads/PeerDrop`. This will be fixed in a future version.

### Recommendations

- ✅ Safe for: family photos, videos, large archives, non-critical documents
- ⚠️ Use with caution: internal team files, source code, business documents
- ❌ Do not use for: passwords, private keys, financial data, medical records — **unless you fully control the VPS and trust the network path**

### Future work

The following improvements are planned or under consideration:

- End-to-end encryption between peers (ChaCha20-Poly1305 with per-peer key exchange)
- Auto-rename received files on conflict instead of overwriting
- Auto-clear clipboard after a short timeout
- Optional binding of the TCP listener to the VPN interface only

---

## 🧠 How it works

```
┌─────────────┐         ┌──────────────────┐         ┌─────────────┐
│   Host PC   │◄───────►│   VPS (Ubuntu)   │◄───────►│  Client PC  │
│             │  SSH    │   AmneziaWG      │  UDP    │             │
│  PeerDrop   │────────►│   VPN Server     │◄────────│  PeerDrop   │
│             │ deploy  │  10.x.x.1/24     │  VPN    │             │
└─────────────┘         └──────────────────┘         └─────────────┘
       ▲                                                    ▲
       │                   VPN tunnel                       │
       └────────────────────────────────────────────────────┘
                    (encrypted peer ↔ VPS ↔ peer)
```

1. The host uses SSH to deploy AmneziaWG on a VPS
2. Each peer receives a client config (as an invite token) and connects to the VPN
3. All peers join a virtual LAN (e.g., `10.9.9.0/24`)
4. Peer discovery uses UDP unicast sweep across the subnet
5. File/secret transfer uses TCP directly between peers, routed by the VPS

---

## ❓ FAQ

**Q: Do I need a VPS?**
Only the host needs a VPS. Clients just need the invite token.

**Q: Is this really peer-to-peer?**
For discovery and transfer logic — yes, peers address each other directly. For the network path — traffic is routed through the VPS because it's a hub-and-spoke VPN. If the VPS is compromised, traffic between peers is visible to it.

**Q: Can the VPS owner read my files?**
In the current version — technically yes, if they inspect the decrypted traffic on the VPN interface. This is a known limitation. Do not use a VPS you don't fully trust for sensitive data.

**Q: What if DPI blocks WireGuard?**
AmneziaWG uses obfuscation (fake DNS packets, header randomization) specifically to bypass DPI. That's why we chose it over standard WireGuard.

**Q: Where are received files saved?**
`~/Downloads/PeerDrop` (Windows: `C:\Users\<you>\Downloads\PeerDrop`).

**Q: Can I use this on macOS or Linux?**
The code has stubs for Linux/macOS, but only Windows is tested. Contributions welcome.

**Q: The app says "wintun.dll not found".**
Make sure `wintun.dll` is in the **same folder** as `peerdrop-awg.exe`. It's included in the release zip.

**Q: Deployment takes forever / server reboots.**
Normal. The installer may reboot 2–3 times. Wait up to 10 minutes. The app reconnects automatically.

**Q: I get "Access denied" when connecting.**
Run the app as **administrator**. Creating a TUN adapter requires elevated privileges.

**Q: Where are my configs stored?**
In `%APPDATA%\PeerDrop\` (Windows). Delete that folder to fully reset the app.

---

## 🛠 Tech Stack

- **[Go 1.27+](https://go.dev/)** — core language
- **[Fyne](https://fyne.io/)** — cross-platform GUI toolkit
- **[amneziawg-go](https://github.com/amnezia-vpn/amneziawg-go)** — WireGuard fork with obfuscation
- **[wintun](https://www.wintun.net/)** — Windows TUN adapter driver
- **[goph](https://github.com/melbahja/goph)** — SSH client for remote deployment

---

## 💖 Support the Project

If this tool saved you time and you'd like to say thanks:

- ⭐ **Star the repo** — it helps other people find it
- 🐛 **Report bugs** or suggest features in [Issues](https://github.com/Igor-Shelloff/peerdrop-awg/issues)
- 💰 **Donate** — [Boosty](https://boosty.to/igorshelloffdev)
- 📩 **Contact** — [@igor_shelloff](https://t.me/igor_shelloff)

Every star and issue helps make the project better.

---

## 📄 License

This project is licensed under the **MIT License** — see the [LICENSE](LICENSE) file for details.

You're free to use, modify and distribute it, including commercially. Attribution is appreciated but not required.

---

## 👤 Author

**Igor Shilov** — Full-Stack Developer

- GitHub: [@Igor-Shelloff](https://github.com/Igor-Shelloff)
- Telegram: [@igor_shelloff](https://t.me/igor_shelloff)
- Email: igor.shilov93@mail.ru
