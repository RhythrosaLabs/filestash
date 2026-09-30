# plg_widget_ai

A built-in assistant (✦ button on the files page, or `ctrl+k`). It can search, read, analyse, rename, move and
de-duplicate the files of the storage you are connected to. It also works on email (`imap`) and calendar (`caldav`)
connections, because emails and events show up as files there.

## Setup

Admin console → Settings → `features > ai`:

| Setting | Free / local option | Hosted option |
|---|---|---|
| base_url | Ollama: `http://localhost:11434/v1` | DeepSeek: `https://api.deepseek.com/v1`, OpenRouter: `https://openrouter.ai/api/v1` |
| model | `qwen3:8b`, `hermes3`, `llama3.1` | `deepseek-chat`, or any `:free` model with tool support on OpenRouter |
| api_key | leave empty | your key |

The model must support **tool calling**. On ollama.com, only pick models that have the `tools` tag. Some Gemma
versions don't support tools and won't be able to act on your files.

```sh
ollama pull qwen3:8b   # ~5GB, runs on most laptops. Use qwen3:14b or hermes3:8b if you have the memory.
```

## What it can do

- `list_dir`, `read_file`, `search` (recursive, by name)
- `find_duplicates`: groups files by size, then by sha256
- `make_dir`, `move` (move or rename): applied right away and listed in the chat
- `delete`: never runs on its own. It shows a confirm button, and only your click deletes the file.
- `remember` / `forget`: long-term memory. The assistant saves your preferences and conventions, and it also sees a
  journal of its recent actions, so it adapts to how you organise things over time. Click 🧠 to review or delete
  what it remembers.

It runs as the logged-in user, through the same permission checks as the rest of Filestash.

## Tip: one view of every cloud

A connection targets one storage at a time. To let the assistant (and find_duplicates) work across all your clouds
at once, create an rclone `combine` remote and connect to it with the rclone backend:

```ini
[everything]
type = combine
upstreams = icloud=icloud: dropbox=dropbox-personal: dropbox-work=dropbox-work: gdrive=gdrive:
```
