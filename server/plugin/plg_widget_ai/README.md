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
| YouTube | a Google OAuth client (steps below), then "Sign in with Google" | yes |

**Instagram downloads the images itself**, so Filestash must be reachable from the internet (admin › settings ›
general › host). A tunnel such as Cloudflare Tunnel or Tailscale Funnel works. Filestash only serves the media of
posts that haven't been published yet, at random 128-bit URLs. Reading Instagram DMs needs Meta app review and isn't
supported. Comments on your recent posts are.

X/Twitter isn't included because its API is paid.

### YouTube setup (once, about 10 minutes)

1. Go to https://console.cloud.google.com and create a project.
2. **APIs & Services › Library**: search "YouTube Data API v3" and click **Enable**.
3. **Google Auth Platform › Audience** (called "OAuth consent screen" in older consoles): choose **External**, fill in
   the app name and your email, and add your Google address as a test user. Then click **Publish app**. While the app
   is in "Testing" mode, Google signs you out after 7 days. Once published, you get a "Google hasn't verified this
   app" warning when you sign in; that's expected for a personal app, so click *Advanced › Continue*.
4. **Credentials › Create credentials › OAuth client ID › Web application**. Under *Authorized redirect URIs* add
   `http://localhost:8334/api/plg_widget_ai/social/oauth/callback`. The 🔗 form shows the exact address for your
   setup. Open Filestash as `localhost`, not `127.0.0.1`, so they match.
5. In the assistant: 🔗 › youtube › paste the client ID and secret › **Connect**, then sign in with Google in the tab
   that opens.

Then, for example: "upload `/Videos/tour.mp4` to YouTube, title *Studio tour*, write a description". The first line
of the post is the title, the rest is the description. "What's new on my socials?" includes comments on your 5
latest videos. Routines work too: "every Friday post the next video of `/Videos/Shorts` to YouTube". Vertical
videos under 3 minutes become Shorts automatically.

Good to know:

- Uploads are **private** by default. Change *privacy* to `unlisted` or `public` when connecting.
- YouTube keeps videos uploaded through the API by a new, unaudited Google project **private**, whatever the setting,
  until the project passes YouTube's API audit (a form in the Google Cloud console). Until then, publish them from
  YouTube Studio with one click.
- The free API quota covers a handful of uploads a day.
- Videos are streamed from disk, up to 4 GB each.

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

## Jev (optional)

[TypeSafe Jev](https://typesafe.ai) is a "System One" model. Instead of writing text, it answers typed questions
(pick one of, score, yes/no) with a confidence, in well under a second and for a fraction of a chat model's price.
With `jev_api_key` set:

- the assistant gets a `classify_files` tool: "sort my Downloads into Invoices, Photos, Code and Other" classifies
  every file in parallel and flags uncertain ones (under 60% confidence) before anything is moved
- `social_inbox` marks each notification with "needs a reply" and an urgency level (can wait, this week, today)

It calls `POST https://api.typesafe.ai/v1/systemone`, following the schema of TypeSafe's published OpenAPI spec.

## External agents (DeepSeek Harness, Hermes Agent, Claude Desktop, …)

🔌 in the assistant gives a token and ready-to-paste configs for Filestash's MCP server (enable it in admin ›
features › mcp). Besides the original SSE endpoint (`/sse`), there's now a Streamable HTTP endpoint (`/mcp`), which
is what DeepSeek Harness and other recent MCP clients use. Agents are confined to the folder of the session the
token was made from.
