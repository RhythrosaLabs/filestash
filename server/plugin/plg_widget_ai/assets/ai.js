// AI assistant panel for the files page. Plain DOM, no dependency.
const API = new URL("../../../api/plg_widget_ai/", import.meta.url).toString();
const STORE = "plg_widget_ai_history";

const api = (path, opts = {}) => fetch(API + path, {
    credentials: "same-origin",
    ...opts,
    headers: { "X-Requested-With": "XmlHttpRequest", "Content-Type": "application/json", ...(opts.headers || {}) },
}).then((r) => r.json()).then((r) => {
    if (r.status !== "ok") throw new Error(r.message || "error");
    return r.result || r.results;
});
const rmFile = (path) => fetch(new URL("../../../api/files/rm?path=" + encodeURIComponent(path), import.meta.url), {
    method: "POST", credentials: "same-origin", headers: { "X-Requested-With": "XmlHttpRequest" },
}).then((r) => r.json()).then((r) => { if (r.status !== "ok") throw new Error(r.message || "error"); });

const currentPath = () => {
    const m = location.pathname.match(/\/files(\/.*)$/);
    return m ? decodeURIComponent(m[1]) : "/";
};
const load = () => { try { return JSON.parse(sessionStorage.getItem(STORE)) || []; } catch (e) { return []; } };
const save = (h) => { try { sessionStorage.setItem(STORE, JSON.stringify(h.slice(-40))); } catch (e) {} };
const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" }[c]));
const fmt = (s) => esc(s)
    .replace(/```([\s\S]*?)```/g, "<pre>$1</pre>")
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<b>$1</b>")
    .replace(/(^|[\s(])\*([^*\n]+)\*(?=[\s.,;:!?)]|$)/gm, "$1<i>$2</i>")
    .replace(/\n/g, "<br>");

const icon = (d) => `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;
const ICONS = {
    accounts: icon('<path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7"/><path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7"/>'),
    queue: icon('<rect x="3" y="4" width="18" height="18" rx="3"/><path d="M16 2v4M8 2v4M3 10h18"/>'),
    memory: icon('<path d="M9 18h6M10 22h4M12 2a7 7 0 0 0-4 12.7V17h8v-2.3A7 7 0 0 0 12 2z"/>'),
    agent: icon('<path d="M12 22v-5M9 8V2M15 8V2M18 8v5a6 6 0 0 1-12 0V8z"/>'),
    clear: icon('<path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/>'),
    close: icon('<path d="M18 6 6 18M6 6l12 12"/>'),
    send: icon('<path d="M5 12h14M13 6l6 6-6 6"/>'),
    spark: `<svg viewBox="0 0 24 24" width="24" height="24" aria-hidden="true"><path fill="currentColor" d="M12 2c.4 4.6 2.9 7.6 8 8.5v1c-5.1.9-7.6 3.9-8 8.5h-1c-.4-4.6-2.9-7.6-8-8.5v-1c5.1-.9 7.6-3.9 8-8.5z"/></svg>`,
};
const SUGGESTIONS = ["Find duplicates here", "Tidy up this folder", "What's new on my socials?", "Summarize the newest file"];

export default function() {
    if (document.querySelector(".plg_widget_ai")) return;
    let history = load();
    let busy = false;

    const $root = document.createElement("div");
    $root.className = "plg_widget_ai";
    $root.innerHTML = `
        <style>${CSS}</style>
        <button class="ai-fab" title="Assistant (ctrl+k)" aria-label="Assistant">${ICONS.spark}</button>
        <section class="ai-panel hidden" aria-label="Assistant">
            <header>
                <strong><span class="ai-spark">${ICONS.spark}</span> Assistant</strong>
                <span>
                    <button data-act="accounts" title="Social accounts">${ICONS.accounts}</button>
                    <button data-act="queue" title="Scheduled posts and routines">${ICONS.queue}</button>
                    <button data-act="memory" title="What I remember">${ICONS.memory}</button>
                    <button data-act="agent" title="Connect an external agent (DeepSeek Harness, Hermes, Claude…)">${ICONS.agent}</button>
                    <button data-act="clear" title="New conversation">${ICONS.clear}</button>
                    <button data-act="close" title="Close">${ICONS.close}</button>
                </span>
            </header>
            <div class="ai-log" aria-live="polite"></div>
            <form>
                <textarea rows="1" placeholder="Ask about your files or socials…"></textarea>
                <button type="submit" title="Send" aria-label="Send">${ICONS.send}</button>
            </form>
        </section>`;
    document.body.appendChild($root);
    const $panel = $root.querySelector(".ai-panel");
    const $log = $root.querySelector(".ai-log");
    const $input = $root.querySelector("textarea");
    const toggle = (show) => {
        $panel.classList.toggle("hidden", show === undefined ? undefined : !show);
        if (!$panel.classList.contains("hidden")) $input.focus();
    };
    const add = (role, html) => {
        const $m = document.createElement("div");
        $m.className = "ai-msg " + role;
        $m.innerHTML = html;
        $log.appendChild($m);
        $log.scrollTop = $log.scrollHeight;
        return $m;
    };
    const renderHistory = () => {
        $log.innerHTML = "";
        if (history.length === 0) {
            const $hello = add("assistant ai-hello", `<h2>Hi there</h2><p>I can search, sort, de-duplicate and post your files, and read what's new on your socials.</p>`);
            const $chips = document.createElement("div");
            $chips.className = "ai-chips";
            SUGGESTIONS.forEach((text) => {
                const $c = document.createElement("button");
                $c.textContent = text;
                $c.onclick = () => send(text);
                $chips.appendChild($c);
            });
            $hello.appendChild($chips);
        }
        history.forEach((m) => add(m.role, fmt(m.content)));
    };

    const renderResult = (res) => {
        const $m = add("assistant", fmt(res.reply));
        if (res.steps && res.steps.length) {
            const $d = document.createElement("details");
            $d.innerHTML = `<summary>${res.steps.length} step(s)</summary><pre>${esc(res.steps.join("\n"))}</pre>`;
            $m.appendChild($d);
        }
        if (res.actions && res.actions.length) {
            const $a = document.createElement("div");
            $a.className = "ai-actions";
            $a.innerHTML = res.actions.map((a) => a.op === "move"
                ? `✔ moved <code>${esc(a.from)}</code> → <code>${esc(a.to)}</code>`
                : `✔ ${esc(a.op)} <code>${esc(a.path)}</code>`).join("<br>")
                + `<br><button data-act="reload">Refresh view</button>`;
            $m.appendChild($a);
        }
        (res.drafts || []).forEach((d) => $m.appendChild(renderDraft(d)));
        (res.pending || []).forEach((p) => {
            const $c = document.createElement("div");
            $c.className = "ai-confirm";
            $c.innerHTML = `Delete <code>${esc(p.path)}</code>? <button data-yes>Delete</button> <button data-no>Keep</button>`;
            $c.querySelector("[data-yes]").onclick = () => rmFile(p.path)
                .then(() => { $c.innerHTML = `🗑 deleted <code>${esc(p.path)}</code>`; history.push({ role: "assistant", content: "Deleted " + p.path }); save(history); })
                .catch((err) => { $c.innerHTML = `⚠️ ${esc(err.message)}`; });
            $c.querySelector("[data-no]").onclick = () => { $c.innerHTML = `kept <code>${esc(p.path)}</code>`; };
            $m.appendChild($c);
        });
    };

    const when = (unix) => unix ? new Date(unix * 1000).toLocaleString() : "now";
    const renderDraft = (d) => {
        const $c = document.createElement("div");
        $c.className = "ai-confirm ai-draft";
        const media = (d.media || []).length ? `<br>📎 ${d.media.length} attachment(s)` : "";
        $c.innerHTML = `<b>${esc(d.account)}</b> · ${d.scheduled_at ? "scheduled " + esc(when(d.scheduled_at)) : "publish on approval"}
            <blockquote>${fmt(d.text)}</blockquote>${media}
            <div><button data-yes>${d.scheduled_at ? "Approve" : "Publish"}</button> <button data-no>Discard</button></div>`;
        $c.querySelector("[data-yes]").onclick = (e) => {
            e.target.disabled = true;
            e.target.textContent = "…";
            api("social/posts/approve?id=" + d.id, { method: "POST" })
                .then((p) => { $c.innerHTML = p.url ? `✔ published: <a href="${esc(p.url)}" target="_blank" rel="noopener">${esc(p.url)}</a>` : `✔ scheduled for ${esc(when(p.scheduled_at))}`; })
                .catch((err) => { $c.innerHTML = `⚠️ ${esc(err.message)}`; });
        };
        $c.querySelector("[data-no]").onclick = () => api("social/posts?id=" + d.id, { method: "DELETE" })
            .then(() => { $c.innerHTML = "discarded"; })
            .catch((err) => { $c.innerHTML = `⚠️ ${esc(err.message)}`; });
        return $c;
    };

    const LABELS = {
        handle: "Handle, eg: you.bsky.social", app_password: "App password (Settings › Privacy › App passwords)", service: "PDS url (optional)",
        instance: "Instance, eg: mastodon.social", access_token: "Access token",
        account_id: "Instagram business account id", graph_url: "Graph API url (optional)",
        client_id: "Google OAuth client ID", client_secret: "Google OAuth client secret", privacy: "Upload as: private (default), unlisted or public",
    };
    const HINTS = {
        youtube: () => `Needs an OAuth client from a Google Cloud project with the YouTube Data API enabled.
            Add this exact redirect URI to the client: <code>${esc(new URL("api/plg_widget_ai/social/oauth/callback", document.baseURI).href)}</code>.
            Step by step: see the assistant's README.`,
        instagram: () => "Instagram downloads images itself: Filestash must be reachable from the internet.",
        bluesky: () => "Create an app password in Bluesky › Settings › Privacy and security › App passwords.",
        mastodon: () => "Preferences › Development › New application with read and write scopes, then copy its access token.",
    };
    const showAccounts = () => api("social/accounts").then(({ accounts, providers }) => {
        const $m = add("assistant", "<b>Social accounts</b>");
        accounts.forEach((a) => {
            const $row = document.createElement("div");
            $row.className = "ai-mem";
            $row.innerHTML = `<span>#${a.id} ${esc(a.provider)} ${esc(a.name)}</span> <button title="Disconnect">✕</button>`;
            $row.querySelector("button").onclick = () => confirm(`Disconnect ${a.name}? Its routines are removed too.`) &&
                api("social/accounts?id=" + a.id, { method: "DELETE" }).then(() => $row.remove());
            $m.appendChild($row);
        });
        const $form = document.createElement("form");
        $form.className = "ai-account-form";
        $form.innerHTML = `<select>${Object.keys(providers).sort().map((p) => `<option>${p}</option>`).join("")}</select><div data-fields></div><button type="submit">Connect</button>`;
        const $fields = $form.querySelector("[data-fields]");
        const renderFields = () => {
            const provider = $form.querySelector("select").value;
            $fields.innerHTML = providers[provider].map((f) =>
                `<input name="${f}" type="${/password|token|secret/.test(f) ? "password" : "text"}" placeholder="${esc(LABELS[f] || f)}" autocomplete="off">`).join("")
                + (HINTS[provider] ? `<small>${HINTS[provider]()}</small>` : "");
        };
        $form.querySelector("select").onchange = renderFields;
        renderFields();
        $form.onsubmit = (e) => {
            e.preventDefault();
            const creds = {};
            $fields.querySelectorAll("input").forEach(($i) => { creds[$i.name] = $i.value; });
            const provider = $form.querySelector("select").value;
            // sign in pages must open from the click, before the request, or popup blockers stop them
            const popup = provider === "youtube" ? window.open("", "_blank") : null;
            api("social/accounts", { method: "POST", body: JSON.stringify({ provider, creds }) })
                .then((a) => {
                    if (a.auth_url) {
                        if (popup) popup.location.href = a.auth_url;
                        else window.open(a.auth_url, "_blank");
                        $form.replaceWith(Object.assign(document.createElement("div"), { innerHTML: `Finish signing in with Google in the new tab. If it says <i>redirect_uri_mismatch</i>, add <code>${esc(a.redirect_uri)}</code> to your OAuth client.` }));
                        return;
                    }
                    $form.replaceWith(Object.assign(document.createElement("div"), { innerHTML: `✔ connected ${esc(a.name)}` }));
                })
                .catch((err) => { if (popup) popup.close(); alert(err.message); });
        };
        $m.appendChild($form);
    }).catch((err) => add("assistant", "⚠️ " + esc(err.message)));

    const showQueue = () => api("social/queue").then(({ posts, routines }) => {
        const $m = add("assistant", posts.length || routines.length ? "<b>Scheduled posts &amp; routines</b>" : "No posts or routines yet. Try: <i>every Monday at 6pm post the next photo of /Art to instagram</i>");
        routines.forEach((r) => {
            const $row = document.createElement("div");
            $row.className = "ai-mem";
            $row.innerHTML = `<span>🔁 #${r.id} ${esc(r.folder)} → ${esc(r.account)} <code>${esc(r.cron)}</code>${r.review ? " (review)" : ""}</span> <button title="Delete">✕</button>`;
            $row.querySelector("button").onclick = () => api("social/routines?id=" + r.id, { method: "DELETE" }).then(() => $row.remove());
            $m.appendChild($row);
        });
        posts.forEach((p) => {
            if (p.status === "draft") return $m.appendChild(renderDraft(p));
            const $row = document.createElement("div");
            $row.className = "ai-mem";
            const icon = { scheduled: "🕒", posting: "⏳", posted: "✔", failed: "⚠️" }[p.status] || "";
            $row.innerHTML = `<span>${icon} #${p.id} ${esc(p.account)} · ${esc(when(p.scheduled_at))}: ${esc(p.text.slice(0, 80))}
                ${p.url ? `<a href="${esc(p.url)}" target="_blank" rel="noopener">open</a>` : ""}${p.error ? `<br><small>${esc(p.error)}</small>` : ""}</span>`;
            if (p.status === "scheduled" || p.status === "failed") {
                const $b = document.createElement("button");
                $b.textContent = p.status === "failed" ? "Retry" : "Cancel";
                $b.onclick = () => (p.status === "failed"
                    ? api("social/posts/retry?id=" + p.id, { method: "POST" })
                    : api("social/posts?id=" + p.id, { method: "DELETE" }))
                    .then(() => $row.remove()).catch((err) => alert(err.message));
                $row.appendChild($b);
            }
            $m.appendChild($row);
        });
    }).catch((err) => add("assistant", "⚠️ " + esc(err.message)));

    const showAgent = () => api("agent_connect").then((c) => {
        const $m = add("assistant", `<b>Connect an external agent</b><br>
            Gives an agent like DeepSeek Harness, Hermes Agent or Claude Desktop access to this storage through MCP.
            The token grants the same access as your session, so keep it private.`);
        const block = (title, text) => {
            const $d = document.createElement("div");
            $d.className = "ai-mem";
            $d.innerHTML = `<div style="width:100%"><b>${esc(title)}</b> <button>Copy</button><pre>${esc(text)}</pre></div>`;
            $d.querySelector("button").onclick = (e) => navigator.clipboard.writeText(text).then(() => { e.target.textContent = "Copied"; });
            $m.appendChild($d);
        };
        block("DeepSeek Harness (filestash.cordis.yml)", c.dsh_patch);
        block("Other MCP clients (Streamable HTTP)", c.generic_config);
        block("Legacy SSE endpoint", c.sse_url);
    }).catch((err) => add("assistant", "⚠️ " + esc(err.message)));

    const send = (message) => {
        if (busy || !message.trim()) return;
        busy = true;
        add("user", fmt(message));
        const $wait = add("assistant thinking", `<span class="ai-dots"><i></i><i></i><i></i></span>`);
        api("chat", { method: "POST", body: JSON.stringify({ message, history, path: currentPath() }) })
            .then((res) => {
                $wait.remove();
                history.push({ role: "user", content: message }, { role: "assistant", content: res.reply });
                save(history);
                renderResult(res);
            })
            .catch((err) => { $wait.remove(); add("assistant", "⚠️ " + esc(err.message)); })
            .finally(() => { busy = false; });
    };

    const showMemory = () => api("memory").then((mems) => {
        const $m = add("assistant", mems.length ? "<b>What I remember:</b>" : "I don't remember anything yet. Tell me about your preferences and I will learn them.");
        mems.forEach((mem) => {
            const $row = document.createElement("div");
            $row.className = "ai-mem";
            $row.innerHTML = `<span>${esc(mem.content)}</span> <button title="Forget">✕</button>`;
            $row.querySelector("button").onclick = () => api("memory?id=" + mem.id, { method: "DELETE" }).then(() => $row.remove());
            $m.appendChild($row);
        });
    }).catch((err) => add("assistant", "⚠️ " + esc(err.message)));

    $root.querySelector(".ai-fab").onclick = () => toggle();
    $input.addEventListener("input", () => { $input.style.height = "auto"; $input.style.height = Math.min($input.scrollHeight, 160) + "px"; });
    $root.addEventListener("click", (e) => {
        const $btn = e.target.closest && e.target.closest("[data-act]");
        const act = $btn && $btn.getAttribute("data-act");
        if (act === "close") toggle(false);
        else if (act === "clear") { history = []; save(history); renderHistory(); }
        else if (act === "memory") showMemory();
        else if (act === "accounts") showAccounts();
        else if (act === "queue") showQueue();
        else if (act === "agent") showAgent();
        else if (act === "reload") location.reload();
    });
    $root.querySelector("section > form").onsubmit = (e) => { e.preventDefault(); const v = $input.value; $input.value = ""; $input.style.height = "auto"; send(v); };
    $input.onkeydown = (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); $root.querySelector("section > form").requestSubmit(); } };
    window.addEventListener("keydown", (e) => { if (e.key === "k" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); toggle(); } });
    window.addEventListener("message", (e) => { if (e.origin === location.origin && e.data === "plg_widget_ai:accounts") showAccounts(); });
    renderHistory();
}

const CSS = `
.plg_widget_ai {
    --ai-bg: rgba(14, 16, 24, 0.86); --ai-solid: #10121a; --ai-fg: #eceef4; --ai-muted: rgba(255, 255, 255, 0.05);
    --ai-light: #9aa1b2; --ai-border: rgba(255, 255, 255, 0.09); --ai-accent: #8b7bff;
    --ai-gradient: linear-gradient(135deg, #22d3ee 0%, #8b7bff 50%, #f472b6 100%);
    font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", Inter, "Segoe UI", Roboto, sans-serif;
    letter-spacing: -0.01em;
}
.plg_widget_ai button { font: inherit; }
.plg_widget_ai .ai-fab {
    position: fixed; right: 22px; bottom: 90px; z-index: 1000; width: 54px; height: 54px; border-radius: 50%;
    border: none; color: #fff; cursor: pointer; display: grid; place-items: center;
    background: var(--ai-gradient); box-shadow: 0 10px 34px -6px rgba(139, 123, 255, 0.75);
    transition: transform .2s ease, box-shadow .2s ease;
}
.plg_widget_ai .ai-fab::before {
    content: ""; position: absolute; inset: -3px; border-radius: 50%; z-index: -1; filter: blur(10px); opacity: .7;
    background: conic-gradient(from 0deg, #22d3ee, #8b7bff, #f472b6, #22d3ee); animation: ai-spin 6s linear infinite;
}
.plg_widget_ai .ai-fab:hover { transform: scale(1.06); }
@keyframes ai-spin { to { transform: rotate(360deg); } }
.plg_widget_ai .ai-panel {
    position: fixed; right: 22px; bottom: 156px; z-index: 1000; width: min(440px, calc(100vw - 32px)); height: min(640px, calc(100vh - 190px));
    display: flex; flex-direction: column; color: var(--ai-fg); background: var(--ai-bg);
    border: 1px solid var(--ai-border); border-radius: 24px; overflow: hidden;
    box-shadow: 0 40px 100px -30px rgba(0, 0, 0, 0.9), 0 0 0 1px rgba(139, 123, 255, 0.12);
    backdrop-filter: blur(28px) saturate(140%); -webkit-backdrop-filter: blur(28px) saturate(140%);
    animation: ai-in .22s ease-out;
}
@keyframes ai-in { from { opacity: 0; transform: translateY(10px) scale(.98); } }
.plg_widget_ai .ai-panel.hidden { display: none; }
.plg_widget_ai header { display: flex; justify-content: space-between; align-items: center; padding: 14px 14px 12px 18px; border-bottom: 1px solid var(--ai-border); }
.plg_widget_ai header strong { display: flex; align-items: center; gap: 8px; font-size: 15px; font-weight: 650; }
.plg_widget_ai .ai-spark { display: inline-grid; width: 22px; height: 22px; place-items: center; }
.plg_widget_ai .ai-spark svg { width: 20px; height: 20px; }
.plg_widget_ai header strong .ai-spark { color: #a598ff; }
.plg_widget_ai header span { display: flex; gap: 2px; }
.plg_widget_ai header button {
    display: grid; place-items: center; width: 32px; height: 32px; border-radius: 10px; border: none; background: transparent;
    color: var(--ai-light); cursor: pointer; transition: background .15s, color .15s; padding: 0;
}
.plg_widget_ai header button:hover { background: var(--ai-muted); color: var(--ai-fg); }
.plg_widget_ai .ai-log { flex: 1; overflow-y: auto; padding: 18px; font-size: 14.5px; line-height: 1.55; }
.plg_widget_ai .ai-msg { margin: 0 0 16px; word-wrap: break-word; }
.plg_widget_ai .ai-msg.user {
    margin-left: auto; max-width: 85%; width: fit-content; padding: 10px 14px; border-radius: 18px 18px 6px 18px;
    background: rgba(139, 123, 255, 0.16); border: 1px solid rgba(139, 123, 255, 0.28);
}
.plg_widget_ai .ai-msg.assistant { padding: 0 2px; }
.plg_widget_ai .ai-hello h2 {
    display: inline-block; margin: 8px 0 4px; font-size: 30px; font-weight: 750; letter-spacing: -0.03em; line-height: 1.15;
    background: var(--ai-gradient); -webkit-background-clip: text; background-clip: text; color: transparent;
}
.plg_widget_ai .ai-hello p { margin: 0 0 14px; color: var(--ai-light); }
.plg_widget_ai .ai-chips { display: flex; flex-wrap: wrap; gap: 8px; }
.plg_widget_ai .ai-msg .ai-chips button {
    border: 1px solid var(--ai-border); background: var(--ai-muted); color: var(--ai-fg); border-radius: 999px;
    padding: 8px 13px; font-size: 13px; cursor: pointer; transition: border-color .15s, background .15s;
}
.plg_widget_ai .ai-msg .ai-chips button:hover { border-color: rgba(139, 123, 255, 0.6); background: rgba(139, 123, 255, 0.12); }
.plg_widget_ai .ai-dots { display: inline-flex; gap: 5px; padding: 6px 0; }
.plg_widget_ai .ai-dots i { width: 7px; height: 7px; border-radius: 50%; background: var(--ai-gradient); animation: ai-bounce 1s infinite ease-in-out; }
.plg_widget_ai .ai-dots i:nth-child(2) { animation-delay: .15s; }
.plg_widget_ai .ai-dots i:nth-child(3) { animation-delay: .3s; }
@keyframes ai-bounce { 0%, 80%, 100% { opacity: .3; transform: translateY(0); } 40% { opacity: 1; transform: translateY(-4px); } }
.plg_widget_ai pre { white-space: pre-wrap; word-break: break-all; font-size: 12px; margin: 6px 0 0; color: var(--ai-light); }
.plg_widget_ai code { font-size: 12.5px; font-family: "SF Mono", ui-monospace, Menlo, Consolas, monospace; color: #c9c2ff; }
.plg_widget_ai .ai-mem code { white-space: nowrap; }
.plg_widget_ai .ai-log a { color: #a598ff; word-break: break-all; }
.plg_widget_ai details { margin-top: 8px; font-size: 12.5px; color: var(--ai-light); }
.plg_widget_ai details summary { cursor: pointer; }
.plg_widget_ai .ai-actions, .plg_widget_ai .ai-confirm, .plg_widget_ai .ai-mem {
    margin-top: 10px; padding: 12px 14px; font-size: 13.5px; border-radius: 16px;
    background: var(--ai-muted); border: 1px solid var(--ai-border);
}
.plg_widget_ai .ai-mem { display: flex; justify-content: space-between; align-items: center; gap: 10px; }
.plg_widget_ai .ai-draft { border-color: rgba(139, 123, 255, 0.35); background: linear-gradient(180deg, rgba(139, 123, 255, 0.10), rgba(255, 255, 255, 0.03)); }
.plg_widget_ai blockquote { margin: 8px 0; padding: 10px 12px; border-left: 3px solid #8b7bff; background: rgba(0, 0, 0, 0.25); border-radius: 8px; }
.plg_widget_ai .ai-msg button {
    margin: 6px 6px 0 0; padding: 7px 14px; cursor: pointer; font-size: 13px; color: var(--ai-fg);
    background: rgba(255, 255, 255, 0.06); border: 1px solid var(--ai-border); border-radius: 999px; text-transform: none;
}
.plg_widget_ai .ai-msg button[data-yes][data-yes] { background: var(--ai-gradient); border-color: transparent; color: #fff; font-weight: 600; }
.plg_widget_ai .ai-account-form { display: flex; flex-direction: column; gap: 8px; padding: 10px 0 0; border: none; }
.plg_widget_ai .ai-account-form input, .plg_widget_ai .ai-account-form select {
    padding: 10px 12px; border: 1px solid var(--ai-border); border-radius: 12px; background: rgba(0, 0, 0, 0.25); color: inherit; font: inherit;
}
.plg_widget_ai .ai-account-form [data-fields] { display: flex; flex-direction: column; gap: 8px; }
.plg_widget_ai .ai-account-form button { align-self: flex-start; }
.plg_widget_ai .ai-account-form small { color: var(--ai-light); line-height: 1.45; }
.plg_widget_ai > section > form { display: flex; align-items: flex-end; gap: 8px; margin: 0 14px 14px; padding: 8px 8px 8px 14px; border: 1px solid var(--ai-border); border-radius: 22px; background: rgba(0, 0, 0, 0.3); transition: border-color .2s, box-shadow .2s; }
.plg_widget_ai > section > form:focus-within { border-color: rgba(139, 123, 255, 0.6); box-shadow: 0 0 0 4px rgba(139, 123, 255, 0.15); }
.plg_widget_ai > section > form textarea { flex: 1; resize: none; border: none; outline: none; padding: 7px 0; font: inherit; font-size: 14.5px; background: transparent; color: inherit; max-height: 160px; }
.plg_widget_ai > section > form textarea::placeholder { color: #6b7385; }
.plg_widget_ai > section > form button { flex: none; display: grid; place-items: center; width: 36px; height: 36px; border: none; border-radius: 50%; background: var(--ai-gradient); color: #fff; cursor: pointer; }
`;
