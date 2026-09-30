# plg_backend_rclone

Mount any [rclone](https://rclone.org) remote as a Filestash storage: iCloud Drive, OneDrive, Box,
pCloud, Mega, extra Dropbox / Google Drive accounts and the other 70+ services rclone supports.

## Setup

1. Install rclone >= 1.69 on the server (`curl https://rclone.org/install.sh | bash`). Distribution
   packages are often too old for iCloud Drive.
2. Create your remotes with `rclone config` (e.g. `icloud`, `dropbox-work`, `gdrive-personal`).
3. Start Filestash with:
   - `RCLONE_CONFIG`: path to the rclone config file (optional, rclone default otherwise)
   - `RCLONE_BINARY`: path to the rclone executable (optional, default: `rclone` from `$PATH`)
   - `RCLONE_BACKEND_SECRET`: shared secret to connect (optional, the admin password always works)
4. In the admin console, add an `rclone` connection per remote. Prefill `remote` and `password` in the
   connection settings so they don't have to be typed at login.

## Security

The rclone config holds the credentials of every remote, so a connection requires the admin password
or `RCLONE_BACKEND_SECRET`. Only remotes declared in the config file are accepted: on the fly
connection strings like `:local:` are rejected, and paths can't escape the optional root `path`.
