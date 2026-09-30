#!/bin/sh
# code generation before the build. The console plugin downloads xterm from
# cdnjs, when that CDN isn't reachable the same files come from the npm registry
set -e
git config --global --add safe.directory "$(pwd)" 2>/dev/null || true
if ! go generate ./server/... ; then
    echo "==> cdnjs unreachable, fetching xterm 3.12.2 from the npm registry"
    rm -rf /tmp/xterm && mkdir -p /tmp/xterm
    curl -fsSL https://registry.npmjs.org/xterm/-/xterm-3.12.2.tgz | tar xz -C /tmp/xterm
    for dir in server/plugin/plg_handler_console/src server/plugin/plg_widget_console/assets/vendor; do
        mkdir -p "$dir"
        cat /tmp/xterm/package/dist/xterm.js /tmp/xterm/package/dist/addons/fit/fit.js > "$dir/xterm.js"
        cp /tmp/xterm/package/dist/xterm.css "$dir/xterm.css"
    done
    go generate ./server/pkg/... ./server/plugin/plg_image_c/...
fi
