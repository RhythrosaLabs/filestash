#!/bin/sh
# Add cloud accounts (iCloud Drive, Dropbox, Google Drive, OneDrive, ...) with
# rclone's interactive setup. Accounts are saved in home/rclone/rclone.conf.
cd "$(dirname "$0")"
docker compose exec -it filestash rclone config
echo "==> done. In Filestash pick 'Clouds (rclone)', type the remote name you just created and your admin password"
