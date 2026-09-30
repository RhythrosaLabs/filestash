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
                    <button data-act="memory" title="What I remember">🧠</button>
                    <button data-act="clear" title="New conversation">⟲</button>
                    <button data-act="close" title="Close">✕</button>
                </span>
            </header>
            <div class="ai-log" aria-live="polite"></div>
            <form>
                <textarea rows="2" placeholder="Ask about your files, eg: find duplicates here, sort my downloads by type…"></textarea>
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
        else if (act === "reload") location.reload();
    });
    $root.querySelector("form").onsubmit = (e) => { e.preventDefault(); const v = $input.value; $input.value = ""; send(v); };
    $input.onkeydown = (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); $root.querySelector("form").requestSubmit(); } };
    window.addEventListener("keydown", (e) => { if (e.key === "k" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); toggle(); } });
    renderHistory();
}

const CSS = `
.plg_widget_ai { --ai-bg: #fff; --ai-fg: #373a3c; --ai-muted: #f2f3f5; --ai-accent: #466372; --ai-border: #e2e2e2; }
@media (prefers-color-scheme: dark) { .plg_widget_ai { --ai-bg: #1f2124; --ai-fg: #e6e6e6; --ai-muted: #2b2e32; --ai-accent: #8fb3c5; --ai-border: #3a3d42; } }
.plg_widget_ai .ai-fab { position: fixed; right: 20px; bottom: 20px; z-index: 1000; width: 48px; height: 48px; border-radius: 50%; border: none; background: var(--ai-accent); color: #fff; font-size: 20px; cursor: pointer; box-shadow: 0 4px 14px rgba(0,0,0,.25); }
.plg_widget_ai .ai-panel { position: fixed; right: 20px; bottom: 80px; z-index: 1000; width: min(420px, calc(100vw - 32px)); height: min(600px, calc(100vh - 120px)); display: flex; flex-direction: column; background: var(--ai-bg); color: var(--ai-fg); border: 1px solid var(--ai-border); border-radius: 10px; box-shadow: 0 10px 30px rgba(0,0,0,.25); overflow: hidden; }
.plg_widget_ai .ai-panel.hidden { display: none; }
.plg_widget_ai header { display: flex; justify-content: space-between; align-items: center; padding: 10px 12px; border-bottom: 1px solid var(--ai-border); }
.plg_widget_ai header button { background: none; border: none; color: inherit; cursor: pointer; font-size: 15px; padding: 2px 6px; }
.plg_widget_ai .ai-log { flex: 1; overflow-y: auto; padding: 12px; font-size: 14px; line-height: 1.45; }
.plg_widget_ai .ai-msg { margin: 0 0 10px; padding: 8px 10px; border-radius: 8px; background: var(--ai-muted); word-wrap: break-word; }
.plg_widget_ai .ai-msg.user { background: var(--ai-accent); color: #fff; margin-left: 40px; }
.plg_widget_ai .ai-msg.thinking { opacity: .6; font-style: italic; }
.plg_widget_ai pre { white-space: pre-wrap; font-size: 12px; margin: 6px 0 0; }
.plg_widget_ai code { font-size: 12px; }
.plg_widget_ai details { margin-top: 6px; font-size: 12px; opacity: .8; }
.plg_widget_ai .ai-actions, .plg_widget_ai .ai-confirm, .plg_widget_ai .ai-mem { margin-top: 8px; padding-top: 6px; border-top: 1px dashed var(--ai-border); font-size: 13px; }
.plg_widget_ai .ai-msg button { margin-top: 4px; cursor: pointer; }
.plg_widget_ai form { display: flex; gap: 8px; padding: 10px; border-top: 1px solid var(--ai-border); }
.plg_widget_ai textarea { flex: 1; resize: none; border: 1px solid var(--ai-border); border-radius: 6px; padding: 6px 8px; font: inherit; background: var(--ai-bg); color: inherit; }
.plg_widget_ai form button { border: none; border-radius: 6px; padding: 0 14px; background: var(--ai-accent); color: #fff; cursor: pointer; }
`;
