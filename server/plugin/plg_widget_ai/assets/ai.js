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
    .replace(/\n/g, "<br>");

export default function() {
    if (document.querySelector(".plg_widget_ai")) return;
    let history = load();
    let busy = false;

    const $root = document.createElement("div");
    $root.className = "plg_widget_ai";
    $root.innerHTML = `
        <style>${CSS}</style>
        <button class="ai-fab" title="AI assistant (ctrl+k)" aria-label="AI assistant">✦</button>
        <section class="ai-panel hidden" aria-label="AI assistant">
            <header>
                <strong>Assistant</strong>
                <span>
                    <button data-act="accounts" title="Social accounts">🔗</button>
                    <button data-act="queue" title="Scheduled posts and routines">📅</button>
                    <button data-act="memory" title="What I remember">🧠</button>
                    <button data-act="agent" title="Connect an external agent (DeepSeek Harness, Hermes, Claude…)">🔌</button>
                    <button data-act="clear" title="New conversation">⟲</button>
                    <button data-act="close" title="Close">✕</button>
                </span>
            </header>
            <div class="ai-log" aria-live="polite"></div>
            <form>
                <textarea rows="2" placeholder="Ask about your files or socials…"></textarea>
                <button type="submit">Send</button>
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
        if (history.length === 0) add("assistant", "Hi! I can search, read, analyse, rename, move and de-duplicate files in this storage. What should we do?");
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
            $fields.innerHTML = providers[$form.querySelector("select").value].map((f) =>
                `<input name="${f}" type="${/password|token/.test(f) ? "password" : "text"}" placeholder="${esc(LABELS[f] || f)}" autocomplete="off">`).join("");
        };
        $form.querySelector("select").onchange = renderFields;
        renderFields();
        $form.onsubmit = (e) => {
            e.preventDefault();
            const creds = {};
            $fields.querySelectorAll("input").forEach(($i) => { creds[$i.name] = $i.value; });
            api("social/accounts", { method: "POST", body: JSON.stringify({ provider: $form.querySelector("select").value, creds }) })
                .then((a) => { $form.replaceWith(Object.assign(document.createElement("div"), { innerHTML: `✔ connected ${esc(a.name)}` })); })
                .catch((err) => alert(err.message));
        };
        $m.appendChild($form);
    }).catch((err) => add("assistant", "⚠️ " + esc(err.message)));

    const showQueue = () => api("social/queue").then(({ posts, routines }) => {
        const $m = add("assistant", posts.length || routines.length ? "" : "No posts or routines yet. Try: <i>every Monday at 6pm post the next photo of /Art to instagram</i>");
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
        const $wait = add("assistant thinking", "thinking…");
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
    $root.addEventListener("click", (e) => {
        const act = e.target.getAttribute && e.target.getAttribute("data-act");
        if (act === "close") toggle(false);
        else if (act === "clear") { history = []; save(history); renderHistory(); }
        else if (act === "memory") showMemory();
        else if (act === "accounts") showAccounts();
        else if (act === "queue") showQueue();
        else if (act === "agent") showAgent();
        else if (act === "reload") location.reload();
    });
    $root.querySelector("section > form").onsubmit = (e) => { e.preventDefault(); const v = $input.value; $input.value = ""; send(v); };
    $input.onkeydown = (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); $root.querySelector("section > form").requestSubmit(); } };
    window.addEventListener("keydown", (e) => { if (e.key === "k" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); toggle(); } });
    renderHistory();
}

const CSS = `
.plg_widget_ai { --ai-bg: #fff; --ai-fg: #373a3c; --ai-muted: #f2f3f5; --ai-accent: #466372; --ai-border: #e2e2e2; }
@media (prefers-color-scheme: dark) { .plg_widget_ai { --ai-bg: #1f2124; --ai-fg: #e6e6e6; --ai-muted: #2b2e32; --ai-accent: #8fb3c5; --ai-border: #3a3d42; } }
.plg_widget_ai .ai-fab { position: fixed; right: 20px; bottom: 88px; z-index: 1000; width: 48px; height: 48px; border-radius: 50%; border: none; background: var(--ai-accent); color: #fff; font-size: 20px; cursor: pointer; box-shadow: 0 4px 14px rgba(0,0,0,.25); }
.plg_widget_ai .ai-panel { position: fixed; right: 20px; bottom: 148px; z-index: 1000; width: min(420px, calc(100vw - 32px)); height: min(600px, calc(100vh - 180px)); display: flex; flex-direction: column; background: var(--ai-bg); color: var(--ai-fg); border: 1px solid var(--ai-border); border-radius: 10px; box-shadow: 0 10px 30px rgba(0,0,0,.25); overflow: hidden; }
.plg_widget_ai .ai-panel.hidden { display: none; }
.plg_widget_ai header { display: flex; justify-content: space-between; align-items: center; padding: 10px 12px; border-bottom: 1px solid var(--ai-border); }
.plg_widget_ai header button { background: none; border: none; color: inherit; cursor: pointer; font-size: 15px; padding: 2px 6px; }
.plg_widget_ai .ai-log { flex: 1; overflow-y: auto; padding: 12px; font-size: 14px; line-height: 1.45; }
.plg_widget_ai .ai-msg { margin: 0 0 10px; padding: 8px 10px; border-radius: 8px; background: var(--ai-muted); word-wrap: break-word; }
.plg_widget_ai .ai-msg.user { background: var(--ai-accent); color: #fff; margin-left: 40px; }
.plg_widget_ai .ai-msg.thinking { opacity: .6; font-style: italic; }
.plg_widget_ai pre { white-space: pre-wrap; word-break: break-all; font-size: 12px; margin: 6px 0 0; }
.plg_widget_ai code { font-size: 12px; }
.plg_widget_ai .ai-log a { color: var(--ai-accent); word-break: break-all; }
.plg_widget_ai details { margin-top: 6px; font-size: 12px; opacity: .8; }
.plg_widget_ai .ai-actions, .plg_widget_ai .ai-confirm, .plg_widget_ai .ai-mem { margin-top: 8px; padding-top: 6px; border-top: 1px dashed var(--ai-border); font-size: 13px; }
.plg_widget_ai .ai-msg button { margin: 4px 4px 0 0; padding: 4px 10px; cursor: pointer; font: inherit; font-size: 13px; color: var(--ai-fg); background: var(--ai-bg); border: 1px solid var(--ai-border); border-radius: 5px; text-transform: none; }
.plg_widget_ai .ai-msg button[data-yes] { background: var(--ai-accent); border-color: var(--ai-accent); color: #fff; }
.plg_widget_ai blockquote { margin: 6px 0; padding: 6px 8px; border-left: 3px solid var(--ai-accent); background: var(--ai-bg); border-radius: 4px; }
.plg_widget_ai .ai-account-form { display: flex; flex-direction: column; gap: 6px; padding: 8px 0 0; border: none; }
.plg_widget_ai .ai-account-form input, .plg_widget_ai .ai-account-form select { padding: 6px; border: 1px solid var(--ai-border); border-radius: 6px; background: var(--ai-bg); color: inherit; font: inherit; }
.plg_widget_ai .ai-account-form [data-fields] { display: flex; flex-direction: column; gap: 6px; }
.plg_widget_ai .ai-account-form button { align-self: flex-start; padding: 6px 14px; }
.plg_widget_ai > section > form { display: flex; gap: 8px; padding: 10px; border-top: 1px solid var(--ai-border); }
.plg_widget_ai > section > form textarea { flex: 1; resize: none; border: 1px solid var(--ai-border); border-radius: 6px; padding: 6px 8px; font: inherit; background: var(--ai-bg); color: inherit; }
.plg_widget_ai > section > form button { border: none; border-radius: 6px; padding: 0 14px; background: var(--ai-accent); color: #fff; cursor: pointer; }
`;
