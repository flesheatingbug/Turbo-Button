# Turbo Button

AI-assisted Windows and Linux tune-ups for computer repair technicians. It has two parts:

- **`agent/`** – `TurboButton.exe`, a portable Windows agent written in Go with no dependencies. Run it on the customer's PC and it shows a QR code. Details are in `agent/README.md`.
- The same agent also runs on **Linux** as `TurboButton-x86_64.AppImage` (see below).
- **`android/`** – the Turbo Button companion app. It scans the QR code, keeps your AI keys encrypted in the Android Keystore, runs the session and saves every report on your phone.

Both use the same control page, `agent/web/index.html`. The `.exe` serves it to phone browsers, and the app bundles it.

## What it can do (v0.3)

- **Scan:** hardware and drive health, startup speed and what's slowing it, blue screens with likely causes, CPU and memory pressure, temperatures, junk files, startup apps, bloat, installed programs, security status, update status, and browser/network hijacks (search engine, extensions, forced policies, proxy, DNS, hosts file).
- **Fix:** clean-ups, startup and bloat removal, privacy and ad tweaks, uninstalling junk programs, Windows Update (including drivers), updating all programs (winget), resetting adware hijacks (proxy, DNS, hosts, browser policies), resetting Windows Update, the network stack, the Store and the search index, SFC/DISM, CHKDSK, memory tests, Defender scans (including offline), and a restart at the end.
- **The PC window shows a live activity log**, so the customer can see what's being done.
- **Safety:** every change needs approval on the phone, and a restore point is created first. Drivers, runtimes, Office and security software can't be uninstalled. Proxy, DNS and policy resets are never proposed on business (domain) PCs. Backups of the hosts file and browser policies go to `C:\ProgramData\TurboButton\backups`.

## Getting the APK and EXE (no Android Studio needed)

1. Create a **private** GitHub repo and push this folder to it.
2. GitHub Actions builds both on every push. When the build succeeds they're published to the repo's **Releases → "Latest build"** page as `TurboButton.apk` and `TurboButton.exe`. You can open that page straight from your phone's browser while signed in to GitHub.
3. Install the APK on your phone. You'll need to allow installs from your browser or file manager when prompted.

### Signing key and in-app updates

The signing key is kept encrypted in the repo (`android/app/turbobutton.keystore.enc`), the same way mbrowser does it. Add its passphrase once as a repository secret under **Settings → Secrets and variables → Actions**, named `TURBO_KEYSTORE_PASS`. Every build is then signed with the same key and installs over the previous one.

The app updates itself from the "Latest build" release. It checks when it opens (at most every 6 hours) and twice a day in the background, with a notification. It asks before downloading, then hands the APK to Android's installer. You can turn the automatic check off, or check by hand, under **⚙ → App updates**. The first time, Android asks you to allow Turbo Button to install apps.

If you're moving from a build signed with a throwaway key, uninstall the old app once and install the new build. The updater detects a key mismatch and tells you so, rather than failing.

### Building locally instead

- **Android Studio:** open the `android/` folder and use Run, or Build → Build APK.
- **Command line, with the Android SDK installed:** `cd android && ./gradlew assembleRelease`
- **The EXE:** `cd agent && GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o TurboButton.exe .`

## AI options (you don't need to pay)

| Option | Cost | Privacy | Set up |
|---|---|---|---|
| **Quick plan** button | Free, offline | Nothing leaves the phone | Nothing — built-in rules |
| **Send to AI app** (Plan/Report tabs) | Uses the Claude/ChatGPT plan you already have | Goes to your AI app | Opens the share sheet: pick Claude or ChatGPT and send (the scan goes as a file, the instruction as your message). On the AI's reply tap **Share → Turbo Button**. In a phone browser it's copy/paste instead. |
| **Ollama** on your own PC/server | Free | Stays on your hardware | Provider → Ollama, base `http://<server>:11434`, model e.g. `qwen3.5:9b`. Away from home, reach it via Tailscale/WireGuard. The app forces Ollama to reply in the exact JSON shape. |
| **Google Gemini** free tier | Free (rate-limited) | Weaker free-tier data terms | Key from aistudio.google.com/apikey |
| **OpenRouter** free models | Free (about 50 requests/day) | Free endpoints may log | Key from openrouter.ai, model `openrouter/free` |
| **Claude API** | Paid per use | Anthropic API terms | Key from platform.claude.com |

The report also has a **Basic report (no AI)** button. It builds the report from the work log plus the Quick-plan findings.

## Multiple PCs (app only)

Connect as many PCs as you like: on the **PCs** tab, tap **Scan QR code** for each one. Every PC keeps running its own scan and fixes in the background, the list shows live status for each, and the name in the header switches between them. PCs, plans and report drafts are saved on the phone, so closing the app loses nothing; a PC that drops off the network shows "Not reachable" and reconnects by itself.

## Linux PCs

Download `TurboButton-x86_64.AppImage` from the same release (there are also plain `TurboButton-linux-x86_64` and `TurboButton-linux-arm64` binaries). It's one static file with nothing to install and works on any distro. Run it from a USB stick:

```
chmod +x TurboButton-x86_64.AppImage   # once
./TurboButton-x86_64.AppImage
```

If you double-click it, it opens a terminal window for the QR code and log. It asks for the admin password through the desktop's normal prompt (pkexec), or sudo in a terminal. It then opens the customer screen in Chrome/Chromium/Brave/Edge if one is installed, as the logged-in user.

- **Scan:** distro, kernel and package manager; drive SMART health (with smartmontools: reallocated, pending and uncorrectable sectors, NVMe wear and media errors, temperatures, power-on hours); disk space; failed services; slow boot units (`systemd-analyze`); kernel crashes, out-of-memory kills and disk I/O errors from the journal; app crash dumps; CPU, memory, swap and temperatures; battery wear; pending updates and when the system was last updated; autostart apps; Flatpak/Snap apps; firewall, SSH and services listening on the network; browser extensions, forced browser policies and the hosts file; Snapper/Timeshift snapshots.
- **Fix:** Snapper/Timeshift snapshot before the first change; clean the package cache, journal, app caches, thumbnails, crash dumps, old temp files and Trash; remove orphaned packages and unused Flatpak runtimes; install all updates (pacman, apt, dnf or zypper, plus Flatpak and Snap) and firmware (fwupd); disable autostart apps (by a per-user override, so the system file is untouched); restart, enable or disable services; TRIM; SMART self-test; disk usage; DNS flush; hosts and browser-policy resets (backups go in `/var/lib/turbobutton/backups`); restart.
- **Protected:** core systemd units, the display manager, NetworkManager, dbus, polkit, the firewall and AppArmor/SELinux can't be disabled.
- The phone app detects a Linux PC and switches its Quick plan rules, AI instructions and deep checks (updates, disk usage, and a short SMART test per drive) to Linux.
- Not on Linux yet: resuming automatically after a restart. Run it again after the restart and the app offers to continue the job.

## Resuming jobs

- **After a restart that Turbo Button starts** (Restart the PC, Defender offline scan): it saves the session and reopens by itself when you log back in, with the same session key. The phone reconnects on its own, and the "before" scan and work log carry on. While the restart is pending it leaves three things on the PC: a copy of itself, a session file readable only by administrators, and a one-time logon task, all under `C:\ProgramData\TurboButton`. They're removed the moment it reopens, or when the session ends.
- **Coming back later** (agent closed, a restart you didn't start, the next day): run TurboButton.exe again and scan the new QR code. The app recognises the PC by its Windows machine ID and offers to continue the previous job, keeping the first visit's "before" scan, the plan, the work log and the report details. Unfinished jobs are kept on the phone for 14 days. Pressing **End** marks the job finished.

## Customer screen on the PC

When the agent starts, it also opens a full-window **customer screen** in an app-style Edge (or Chrome) window. It uses the same retro terminal look as the app and shows the QR code until the phone connects, then what's being done and why (with a progress bar), completed fixes, space freed, elapsed time and a "please don't turn off" warning while work runs. It only accepts connections from the PC itself and is read-only. The QR code stays on screen so the phone can reconnect, or another phone can add the PC. Once the screen is up, the console window hides itself. **Closing the screen ends the session** (Windows asks first if work is still running). The exception is while Turbo Button is restarting the PC: then it's ignored, so the session can resume. If neither Edge nor Chrome is installed, the console stays visible as before. Use `--no-screen` to skip the screen, or `--console` to keep the console visible.

## Look

The phone and the PC window use a retro green-phosphor terminal style by default. You can switch to amber or the modern look under ⚙ → Look.

## Using the app

1. Tap **⚙** and choose a provider (Claude, OpenAI, OpenRouter, LM Studio or Ollama), then enter the model and key. They're saved encrypted on the phone and work for every customer.
2. Run `TurboButton.exe` on the customer's PC, tap **Scan QR code** and point the phone at the console window.
3. Work through **Scan → Plan** (tick the actions you approve, then Run) **→ Work → Report**. The report is saved automatically. Use **Share** to email it or send it on WhatsApp, Drive and so on.
4. **End** closes the agent on the PC, then **Connect to the next PC** starts a new session.

In the app, all network traffic goes through Android directly rather than through the page. So local AI servers don't need any browser access (CORS) settings, and the agent's plain-HTTP connection on the local network works normally.

## Not yet tested

The Linux agent was tested for real on Ubuntu 24.04 (scan, clean-ups, autostart, deep checks, Quick plan), and CI runs a real scan on every build. It hasn't yet been tried on a desktop with a graphical login, or on Arch, Fedora or openSUSE.


Version 0.1 was built and tested in a simulated setup: the agent runs with fake Windows results, and the control page is tested in Chrome with a stand-in for the Android bridge. It has not yet been run on real Windows or a real Android phone, so try both on your own devices first.
