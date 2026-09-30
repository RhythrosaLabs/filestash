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

It runs as the logged-in user, through the same permission checks as the rest of Filestash. People who open a public
share link don't get the assistant.

## Social media

Connect accounts from the 🔗 button. Credentials are checked when you connect, then stored encrypted on the server.
They're never sent to the model.

| Network | What you need | Free? |
|---|---|---|
| Bluesky | handle + an [app password](https://bsky.app/settings/app-passwords). To read DMs, tick "allow access to direct messages". | yes |
| Mastodon | instance + an access token (Preferences › Development › New application, with scopes `read write`) | yes |
| Instagram | a Business or Creator account linked to a Facebook page, an access token with `instagram_content_publish` and `instagram_basic`, and the Instagram account id | yes, but Meta's developer setup is involved |

**Instagram downloads the images itself**, so Filestash must be reachable from the internet (admin › settings ›
general › host). A tunnel such as Cloudflare Tunnel or Tailscale Funnel works. Filestash only serves the media of
posts that haven't been published yet, at random 128-bit URLs. Reading Instagram DMs needs Meta app review and isn't
supported. Comments on your recent posts are.

X/Twitter isn't included because its API is paid.

Things to ask:

- "Post `/Art/sunset.png` on Bluesky with a caption about autumn": creates a **draft** that you publish with one click.
- "Schedule it for Friday 6pm": the same, and it goes out at that time once you approve it.
- "What's new on my socials?": replies, mentions, likes, follows, messages and comments.
- "Every Monday and Thursday at 6pm, post the next photo from `/Art` to Instagram. Keep it short, add #art
  #painting": creates a **routine**. On schedule it picks the next file of the folder that hasn't been posted yet,
  looks at the image (with a vision model like `qwen2.5vl` or `gemma3`, otherwise from the file name) or reads the
  text file, writes the post in your voice using what it remembers about you, and publishes it. Add "let me review
  them first" to get drafts instead.

📅 shows the queue: drafts to approve, scheduled posts to cancel, failed ones to retry, and routines to remove.
Routines keep an encrypted copy of your storage session so they can read the folder while you're away, the same
way Filestash workflows do.

## Tip: one view of every cloud

A connection targets one storage at a time. To let the assistant (and find_duplicates) work across all your clouds
at once, create an rclone `combine` remote and connect to it with the rclone backend:

```ini
[everything]
type = combine
upstreams = icloud=icloud: dropbox=dropbox-personal: dropbox-work=dropbox-work: gdrive=gdrive:
```
