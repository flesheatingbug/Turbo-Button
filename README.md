# Turbo Button

AI-assisted Windows tune-ups for computer repair technicians. It has two parts:

- **`agent/`** – `TurboButton.exe`, a portable Windows agent written in Go with no dependencies. Run it on the customer's PC and it shows a QR code. Details are in `agent/README.md`.
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

### Stable signing (recommended before relying on it)

Without a signing key, each build is signed with a throwaway key. That means you'd have to uninstall the app before installing an update, which wipes your saved keys and reports. To avoid that, set up a permanent key once:

```
cd android && ./make-signing-key.sh
```

Then add two repository secrets under **Settings → Secrets and variables → Actions**:
- `TURBO_KEYSTORE_B64`: the base64 text the script prints
- `TURBO_KEYSTORE_PASS`: the password you chose

Keep `turbobutton.jks` safe and don't commit it (it's already in `.gitignore`). If you lose it, you can't update the installed app without uninstalling it.

### Building locally instead

- **Android Studio:** open the `android/` folder and use Run, or Build → Build APK.
- **Command line, with the Android SDK installed:** `cd android && ./gradlew assembleRelease`
- **The EXE:** `cd agent && GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o TurboButton.exe .`

## AI options (you don't need to pay)

| Option | Cost | Privacy | Set up |
|---|---|---|---|
| **Quick plan** button | Free, offline | Nothing leaves the phone | Nothing — built-in rules |
| **Copy for AI app** (Plan/Report tabs) | Uses the Claude/ChatGPT plan you already have | Goes to your AI app | Copy → paste into a new chat → paste the reply back |
| **Ollama** on your own PC/server | Free | Stays on your hardware | Provider → Ollama, base `http://<server>:11434`, model e.g. `qwen3.5:9b`. Away from home, reach it via Tailscale/WireGuard. The app forces Ollama to reply in the exact JSON shape. |
| **Google Gemini** free tier | Free (rate-limited) | Weaker free-tier data terms | Key from aistudio.google.com/apikey |
| **OpenRouter** free models | Free (about 50 requests/day) | Free endpoints may log | Key from openrouter.ai, model `openrouter/free` |
| **Claude API** | Paid per use | Anthropic API terms | Key from platform.claude.com |

The report also has a **Basic report (no AI)** button. It builds the report from the work log plus the Quick-plan findings.

## Multiple PCs (app only)

Connect as many PCs as you like: on the **PCs** tab, tap **Scan QR code** for each one. Every PC keeps running its own scan and fixes in the background, the list shows live status for each, and the name in the header switches between them. PCs, plans and report drafts are saved on the phone, so closing the app loses nothing; a PC that drops off the network shows "Not reachable" and reconnects by itself.

## Resuming jobs

- **After a restart that Turbo Button starts** (Restart the PC, Defender offline scan): it saves the session and reopens by itself when you log back in, with the same session key. The phone reconnects on its own, and the "before" scan and work log carry on. While the restart is pending it leaves three things on the PC: a copy of itself, a session file readable only by administrators, and a one-time logon task, all under `C:\ProgramData\TurboButton`. They're removed the moment it reopens, or when the session ends.
- **Coming back later** (agent closed, a restart you didn't start, the next day): run TurboButton.exe again and scan the new QR code. The app recognises the PC by its Windows machine ID and offers to continue the previous job, keeping the first visit's "before" scan, the plan, the work log and the report details. Unfinished jobs are kept on the phone for 14 days. Pressing **End** marks the job finished.

## Customer screen on the PC

When the agent starts, it also opens a full-window **customer screen** in an app-style Edge (or Chrome) window. It uses the same retro terminal look as the app and shows the QR code until the phone connects, then what's being done and why (with a progress bar), completed fixes, space freed, elapsed time and a "please don't turn off" warning while work runs. It only accepts connections from the PC itself and is read-only. Closing it doesn't end the session; closing the console window does. Start the agent with `--no-screen` to skip it.

## Look

The phone and the PC window use a retro green-phosphor terminal style by default. You can switch to amber or the modern look under ⚙ → Look.

## Using the app

1. Tap **⚙** and choose a provider (Claude, OpenAI, OpenRouter, LM Studio or Ollama), then enter the model and key. They're saved encrypted on the phone and work for every customer.
2. Run `TurboButton.exe` on the customer's PC, tap **Scan QR code** and point the phone at the console window.
3. Work through **Scan → Plan** (tick the actions you approve, then Run) **→ Work → Report**. The report is saved automatically. Use **Share** to email it or send it on WhatsApp, Drive and so on.
4. **End** closes the agent on the PC, then **Connect to the next PC** starts a new session.

In the app, all network traffic goes through Android directly rather than through the page. So local AI servers don't need any browser access (CORS) settings, and the agent's plain-HTTP connection on the local network works normally.

## Not yet tested

Version 0.1 was built and tested in a simulated setup: the agent runs with fake Windows results, and the control page is tested in Chrome with a stand-in for the Android bridge. It has not yet been run on real Windows or a real Android phone, so try both on your own devices first.
