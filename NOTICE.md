# Notice

Sensorium is a modified version of **Filestash** (https://github.com/mickael-kerjean/filestash),
Copyright (C) Mickael Kerjean and the Filestash contributors, licensed under the GNU Affero General Public License
version 3 (see [LICENSE](LICENSE)). Sensorium is distributed under the same license.

"Filestash" is the name of the original project. Sensorium is an independent project, not affiliated with or
endorsed by Filestash.

## What Sensorium adds or changes

New plugins:

- `server/plugin/plg_widget_ai`: AI assistant, long-term memory, social media (Bluesky, Mastodon, Instagram,
  YouTube), scheduled posts and routines, TypeSafe Jev integration, external agent connection
- `server/plugin/plg_backend_rclone`: rclone remotes as storage
- `server/plugin/plg_backend_imap`: email (IMAP) as storage
- `server/plugin/plg_backend_caldav`: calendars (CalDAV) as storage
- `server/plugin/plg_theme_sensorium`: dark theme, favicon

Changes to existing code:

- `server/plugin/plg_handler_mcp`: Streamable HTTP transport (`/mcp`), shared request dispatch, tools confined to the
  folder of the session
- `server/plugin/index.go`: enables the plugins above, plus these existing Filestash plugins: `plg_search_sqlitefts`,
  `plg_widget_recent`, `plg_widget_favourite`, `plg_video_thumbnail`
- `home/`: Docker setup, start scripts and first-run configuration
- `README.md`: new README. The original is in `docs/FILESTASH_README.md`
- `go.mod` / `go.sum`: new dependencies (emersion/go-imap, emersion/go-webdav, emersion/go-ical)

The full history of the changes is in the git log.
