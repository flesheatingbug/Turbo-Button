# Turbo Button 0.1.0

A portable Windows tune-up agent for repair technicians, controlled from your phone. You run one `.exe`, scan the QR code, and an AI that you choose (Claude, ChatGPT or a local model) proposes fixes. Nothing runs until you approve it.

## How a session goes

1. Copy `TurboButton.exe` to the customer's PC (USB stick, download, etc.) and run it. Nothing is installed.
2. Accept the administrator prompt. A console window then shows a QR code.
3. Scan the QR code with your phone, which must be on the same network. If the customer's network blocks devices from reaching each other (guest Wi-Fi, for example), connect the PC to your phone's hotspot.
4. **Setup tab:** choose the AI provider and enter your key. Your key only ever travels from your phone to the provider.
5. **Scan → Plan:** the AI reads the scan and proposes actions. Tick the ones you approve, then press **Run**.
6. **Work tab:** shows live progress. Before the first change, the agent automatically creates a System Restore point (you can switch this off in Setup).
7. **Report:** optionally rescan, then press **Write report**. Download or share the HTML report from your phone.
8. **End session** (or close the console window) removes the temporary firewall rule and temporary files, then the agent exits. What stays on the PC: the approved changes and the restore point.

## Safety design

- **The AI can only pick from a fixed list of actions** (`actions.go`). It can never run arbitrary commands. Each action's settings are checked against strict rules and passed to PowerShell as environment variables, so they can't inject commands.
- **Protected items are refused** no matter what is requested: Defender, the firewall, Windows Update and core services, plus the Store, Edge runtime, frameworks and core apps.
- **Access is locked down.** Every request needs a random 128-bit session token, which only the QR code carries. The firewall rule only accepts connections from the local network and is deleted when the session ends.
- **Undo options:** a restore point is made before the first change. `startup_enable` reverses `startup_disable`. Service changes record the previous startup mode.

## AI providers

| Provider | Base URL | Notes |
|---|---|---|
| Claude | `https://api.anthropic.com` | API key from console.anthropic.com. This is pay-per-use, separate from a Claude.ai subscription. |
| OpenAI | `https://api.openai.com/v1` | |
| OpenRouter | `https://openrouter.ai/api/v1` | Gives access to many models with one key |
| LM Studio | `http://<pc-ip>:1234/v1` | Turn on CORS in the server settings |
| Ollama | `http://<pc-ip>:11434/v1` | Start Ollama with `OLLAMA_ORIGINS=*`. Small models may produce weak plans. |

"Remember on this phone" stores the key in the phone browser's storage. That storage is tied to each PC's IP address, so on a new customer's network you may need to enter the key again. The native Android app planned for the next version fixes this.

## Building

```
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o TurboButton.exe .
```
This uses only Go's standard library. To test the API and phone page on Linux with fake results: `TURBO_MOCK=1 go run .`, then open `http://127.0.0.1:47800/#t=<token>`.

## Known limits (v0.1)

- **Not yet tested on a real Windows PC.** The PowerShell scripts in `scripts/` were written for Windows 10/11 with Windows PowerShell 5.1. Do a first run on your own machine.
- **SFC result detection reads English output.** On non-English Windows the verdict shows as "unknown", but the full output is still shown.
- **Per-user settings apply to the account the agent runs under.** Startup items, privacy tweaks and browser-cache paths in HKCU use that account. If you elevate with a different admin account than the customer's, those settings apply to the wrong user. Cleanup covers all user profiles.
- **Not done by the agent:** uninstalling desktop programs, updating drivers and BIOS updates. The AI lists these as manual steps.
- **Windows SmartScreen may warn** because the `.exe` is unsigned. Click "More info → Run anyway", or sign it with a code-signing certificate.
