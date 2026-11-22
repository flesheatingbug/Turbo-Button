# Turbo Button

AI-assisted Windows tune-ups for computer repair technicians. It has two parts:

- **`agent/`** – `TurboButton.exe`, a portable Windows agent written in Go with no dependencies. Run it on the customer's PC and it shows a QR code. Details are in `agent/README.md`.
- **`android/`** – the Turbo Button companion app. It scans the QR code, keeps your AI keys encrypted in the Android Keystore, runs the session and saves every report on your phone.

Both use the same control page, `agent/web/index.html`. The `.exe` serves it to phone browsers, and the app bundles it.

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

## Using the app

1. Open **Setup** and choose a provider (Claude, OpenAI, OpenRouter, LM Studio or Ollama), then enter the model and key. They're saved encrypted on the phone and work for every customer.
2. Run `TurboButton.exe` on the customer's PC, tap **Scan QR code** and point the phone at the console window.
3. Work through **Scan → Plan** (tick the actions you approve, then Run) **→ Work → Report**. The report is saved automatically. Use **Share** to email it or send it on WhatsApp, Drive and so on.
4. **End** closes the agent on the PC, then **Connect to the next PC** starts a new session.

In the app, all network traffic goes through Android directly rather than through the page. So local AI servers don't need any browser access (CORS) settings, and the agent's plain-HTTP connection on the local network works normally.

## Not yet tested

Version 0.1 was built and tested in a simulated setup: the agent runs with fake Windows results, and the control page is tested in Chrome with a stand-in for the Android bridge. It has not yet been run on real Windows or a real Android phone, so try both on your own devices first.
