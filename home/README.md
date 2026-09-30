# Run it at home

Everything runs on your computer in Docker: Filestash, the AI assistant and a local AI model.

## 1. Once: install Docker Desktop

https://www.docker.com/products/docker-desktop/. Open it and wait until it says it's running.

**For the AI, use one of these (fastest first):**

- **LM Studio**, if you already have it: open it, load a model with tool use (e.g. *Qwen3 8B* or *Qwen3 14B*), then
  start the server from the **Developer** tab (or run `lms server start`). The start script detects it and uses the
  loaded model.
- **Ollama app**: install it from [ollama.com/download](https://ollama.com/download) (Mac: `brew install ollama` also
  works), then run `ollama pull qwen3:8b`. The start script detects it too.
- **Neither**: the start script runs Ollama inside Docker and downloads `qwen3:8b` for you. This works everywhere, but
  it's slower on a Mac because Docker can't use the Apple GPU.

On Linux, LM Studio and the Ollama app only listen on localhost, where Docker can't reach them. Either let the script
run Ollama in Docker, or set `AI_BASE_URL` in `home/.env` (for LM Studio, turn on *Serve on Local Network*).

## 2. Start

```sh
git clone https://github.com/RhythrosaLabs/sensorium.git
cd sensorium/home
./start.sh                                               # Mac / Linux
powershell -ExecutionPolicy Bypass -File .\start.ps1     # Windows
```

The first run builds Filestash (5 to 15 minutes) and downloads the AI model (about 5 GB). Later runs start in
seconds. Running the script again after a `git pull` rebuilds with the latest changes.

The browser opens http://localhost:8334:

1. **Choose an admin password.** You land on the admin console. You can ignore the presets it offers.
2. Go to http://localhost:8334/login and pick **This computer**. Log in with that password. You see your home folder.
3. Click **✦** at the bottom right, or press `ctrl+k`, to talk to the assistant.

## 3. Connect your other stuff

| What | How |
|---|---|
| iCloud Drive, Dropbox, Google Drive, OneDrive, Box, … (several accounts each) | `./rclone-config.sh` runs rclone's step-by-step setup. Give each account a name like `icloud` or `dropbox-work`. Then on the login page pick **Clouds (rclone)** and type that name and your admin password. |
| Email (Gmail, iCloud, Outlook, …) | Login page › **Email**: IMAP server (`imap.gmail.com`, `imap.mail.me.com`, …), your address and an *app password* from your email provider's security settings |
| Calendar (iCloud, Fastmail, Nextcloud) | Login page › **Calendar**: e.g. `https://caldav.icloud.com` with your Apple ID and an app-specific password |
| Another computer | Login page › **Other computer (SFTP)**: turn on "Remote Login" (Mac) or OpenSSH server (Windows/Linux) on that computer |
| Phone | Login page › **Phone (WebDAV)**: any WebDAV server app on the phone |
| Social accounts (Bluesky, Mastodon, Instagram, YouTube) | In the assistant, click 🔗. YouTube needs a one-time Google Cloud setup: see [the assistant's README](../server/plugin/plg_widget_ai/README.md#youtube-setup-once-about-10-minutes) |

Tip: in `rclone-config.sh`, a remote of type `combine` merges several clouds into one tree. Connect to that
one and the assistant can search and de-duplicate across all of them at once.

## Settings

Edit `home/.env` and run the start script again:

- `FILES_DIR`: the folder shown as "This computer" (default: your home folder)
- `AI_MODEL`: any Ollama model with tool calling (`qwen3:8b`, `qwen3:14b`, `hermes3`, `llama3.1`)
- `AI_BASE_URL` / `AI_API_KEY`: to use a hosted model instead, e.g. DeepSeek `https://api.deepseek.com/v1` and model `deepseek-chat`
- LM Studio set by hand: `AI_BASE_URL=http://host.docker.internal:1234/v1`, `AI_MODEL=` the model id shown in LM Studio, `AI_API_KEY=lm-studio`
- `JEV_API_KEY`: optional [TypeSafe Jev](https://typesafe.ai) key. It makes "sort these files" and inbox triage fast and cheap.

The `.env` values are used the first time only. Afterwards, change them in the admin console
(http://localhost:8334/admin › Settings › features › ai).

## Connect DeepSeek Harness (or Hermes Agent, Claude Desktop, …)

In the assistant, click 🔌. It shows a ready-to-use config with a private token for the storage you're logged in to.
For [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness):

```sh
# save the first block from 🔌 as filestash.cordis.yml, then
npx @deepseek-ai/dsh web --patch "$PWD/filestash.cordis.yml"
```

Its agent can then list, read, write, move and delete files as `mcp__filestash__*` tools, confined to that storage.

## Stop / troubleshoot

- Stop everything: `./stop.sh` (Windows: `docker compose --profile ollama down`). Your settings and connections are kept.
- Logs: `docker compose logs -f sensorium`
- Port 8334 already used: change `"8334:8334"` to e.g. `"8400:8334"` in `docker-compose.yml`
- The assistant says it can't reach the model: `docker compose logs ollama-pull`. The model may still be downloading.
