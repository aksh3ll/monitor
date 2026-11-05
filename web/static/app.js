"use strict";

const REFRESH_MS = 5000;
let timer = null;

// ------------------------------------------------------------ DOM helpers

// h("div", {class: "x"}, child1, "text", ...) — text is always set via textContent.
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k === "class") el.className = v;
    else el.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

function fmtBytes(n) {
  if (n === null || n === undefined || n < 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
}

function fmtDuration(sec) {
  if (!sec || sec < 0) return "—";
  const d = Math.floor(sec / 86400), hr = Math.floor(sec % 86400 / 3600), m = Math.floor(sec % 3600 / 60);
  if (d) return `${d}d ${hr}h ${m}m`;
  if (hr) return `${hr}h ${m}m`;
  if (m) return `${m}m ${Math.floor(sec % 60)}s`;
  return `${Math.floor(sec)}s`;
}

function fmtAgo(iso) {
  if (!iso) return "never";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  return s < 60 ? `${Math.round(s)}s ago` : `${fmtDuration(s)} ago`;
}

function fmtTime(iso) {
  return iso ? new Date(iso).toLocaleString() : "never";
}

function level(pct) {
  return pct >= 90 ? "bad" : pct >= 75 ? "warn" : "";
}

function meter(label, pct, detail) {
  const fill = h("div", { class: level(pct) });
  fill.style.width = `${Math.min(100, Math.max(0, pct))}%`;
  return h("div", { class: "meter" },
    h("div", { class: "meter-label" }, h("span", { title: label }, label), h("span", { class: "muted" }, detail)),
    h("div", { class: "bar" }, fill));
}

function kv(pairs) {
  const dl = h("dl", { class: "kv" });
  for (const [k, v] of pairs) {
    if (v === undefined || v === null || v === "") continue;
    dl.append(h("dt", {}, k), h("dd", {}, v));
  }
  return dl;
}

function pill(text, cls) {
  return h("span", { class: `pill ${cls}` }, text);
}

function table(headers, rows, numericCols = []) {
  return h("div", { class: "table-wrap" },
    h("table", {},
      h("thead", {}, h("tr", {}, headers.map((t, i) => h("th", { class: numericCols.includes(i) ? "num" : null }, t)))),
      h("tbody", {}, rows.map(r => h("tr", {}, r.map((c, i) => h("td", { class: numericCols.includes(i) ? "num" : null }, c)))))));
}

// card({title, sub, icon, status, body, entry, wide})
function card(o) {
  const foot = o.entry
    ? h("div", { class: "card-foot" },
        h("span", { title: fmtTime(o.entry.updated_at) }, `updated ${fmtAgo(o.entry.updated_at)}`),
        h("span", {}, `every ${o.entry.interval_seconds}s · ${o.entry.duration_ms}ms`))
    : null;
  return h("article", { class: `card${o.wide ? " wide" : ""}` },
    h("img", { class: "card-icon", src: `icons/${o.icon}.svg`, alt: "" }),
    h("div", { class: "card-head" }, h("h3", { class: "card-title", title: o.title }, o.title), o.status),
    o.sub ? h("div", { class: "card-sub" }, o.sub) : null,
    h("div", { class: "card-body" }, o.body),
    foot);
}

// Wraps a service entry: handles disabled / pending / error states.
function serviceState(entry) {
  if (!entry || !entry.enabled) return { status: pill("disabled", "off"), note: h("div", { class: "muted" }, "Disabled in configuration.") };
  if (!entry.data && entry.error) return { status: pill("error", "bad"), note: h("div", { class: "error" }, entry.error) };
  if (!entry.data) return { status: pill("pending", "off"), note: h("div", { class: "muted" }, "Waiting for first collection…") };
  return { status: null, note: entry.error ? h("div", { class: "error" }, entry.error) : null };
}

// ------------------------------------------------------------ renderers

function renderSystem(entry) {
  const st = serviceState(entry);
  if (!entry || !entry.data) {
    return [card({ title: "Server", icon: "server", status: st.status, body: st.note, entry })];
  }
  const d = entry.data;
  const m = d.memory;
  const cards = [];

  cards.push(card({
    title: d.hostname || "Server", icon: "server", entry,
    status: pill("online", "ok"), sub: d.os,
    body: [
      st.note,
      kv([
        ["Kernel", d.kernel],
        ["Uptime", fmtDuration(d.uptime_seconds)],
        ["CPUs", d.cpu_count],
        ["Load", d.load.map(l => l.toFixed(2)).join(" / ")],
      ]),
      meter("CPU", d.cpu_percent, `${d.cpu_percent}%`),
    ],
  }));

  cards.push(card({
    title: "Memory", icon: "memory", entry,
    status: pill(`${m.used_percent}%`, level(m.used_percent) || "ok"),
    body: [
      meter("RAM", m.used_percent, `${fmtBytes(m.used)} / ${fmtBytes(m.total)}`),
      m.swap_total > 0
        ? meter("Swap", 100 * m.swap_used / m.swap_total, `${fmtBytes(m.swap_used)} / ${fmtBytes(m.swap_total)}`)
        : h("div", { class: "muted small" }, "No swap"),
      kv([["Available", fmtBytes(m.available)]]),
    ],
  }));

  const disks = d.disks || [];
  const worst = Math.max(0, ...disks.map(x => x.used_percent));
  cards.push(card({
    title: "Disks", icon: "disk", entry,
    status: pill(`${disks.length} mounts`, level(worst) || "ok"),
    body: disks.length
      ? disks.map(x => meter(`${x.mount} (${x.fstype})`, x.used_percent, `${fmtBytes(x.used)} / ${fmtBytes(x.total)} · ${x.used_percent}%`))
      : h("div", { class: "muted" }, "No disks found"),
  }));

  const ports = d.listening_ports || [];
  cards.push(card({
    title: "Listening ports", icon: "ports", entry,
    status: pill(`${ports.length}`, "ok"),
    sub: "Sockets listening on this host",
    body: table(["Port", "Proto", "Address"],
      ports.map(p => [p.port, p.proto, h("code", {}, p.address)]), [0]),
  }));
  return cards;
}

function containerStatusPill(state) {
  const cls = { running: "ok", paused: "warn", restarting: "warn", created: "off", exited: "bad", dead: "bad" }[state] || "off";
  return pill(state, cls);
}

function renderDocker(entry) {
  const st = serviceState(entry);
  if (!entry || !entry.data) {
    return [card({ title: "Docker", icon: "docker", status: st.status, body: st.note, entry })];
  }
  const d = entry.data;
  const cards = [];
  cards.push(card({
    title: "Docker engine", icon: "docker", entry,
    status: pill(entry.error ? "stale" : "online", entry.error ? "warn" : "ok"),
    sub: `version ${d.server_version}`,
    body: [
      st.note,
      kv([
        ["Containers", d.containers],
        ["Running", d.running],
        ["Paused", d.paused],
        ["Stopped", d.stopped],
        ["Images", d.images],
        ["Networks", (d.networks || []).length],
      ]),
    ],
  }));

  for (const c of d.container_list || []) {
    const ports = (c.ports || []).map(p => p.public_port
      ? h("span", { class: "chip public", title: "published on host" }, `${p.ip || "*"}:${p.public_port} → ${p.private_port}/${p.type}`)
      : h("span", { class: "chip", title: "internal only" }, `${p.private_port}/${p.type}`));
    const nets = (c.networks || []).map(n => h("span", { class: "chip" }, n.ip ? `${n.name} · ${n.ip}` : n.name));
    cards.push(card({
      title: c.name, icon: "container", entry,
      status: containerStatusPill(c.state),
      sub: c.image,
      body: [
        kv([["Status", c.status], ["ID", h("code", {}, c.id)], ["Created", new Date(c.created).toLocaleString()]]),
        h("div", { class: "muted small" }, "Ports (outside → inside)"),
        ports.length ? h("div", { class: "chips" }, ports) : h("div", { class: "muted small" }, "none"),
        h("div", { class: "muted small" }, "Networks"),
        nets.length ? h("div", { class: "chips" }, nets) : h("div", { class: "muted small" }, "none"),
      ],
    }));
  }

  const nets = d.networks || [];
  cards.push(card({
    title: "Networks", icon: "network", entry, wide: true,
    status: pill(`${nets.length}`, "ok"),
    body: table(["Name", "Driver", "Scope", "Subnet", "Gateway", "Containers"],
      nets.map(n => [
        n.internal ? `${n.name} (internal)` : n.name,
        n.driver, n.scope,
        h("code", {}, (n.subnets || []).join(", ") || "—"),
        h("code", {}, (n.gateways || []).join(", ") || "—"),
        (n.containers || []).join(", ") || "—",
      ])),
  }));
  return cards;
}

function renderDB(entry, title, icon) {
  const st = serviceState(entry);
  if (!entry || !entry.data) {
    return card({ title, icon, status: st.status, body: st.note, entry });
  }
  const d = entry.data;
  const up = d.status === "up";
  const dbs = d.databases || [];
  const hasTables = dbs.some(x => x.tables);
  const hasOwner = dbs.some(x => x.owner);
  const headers = ["Database", "Size"];
  if (hasTables) headers.push("Tables");
  if (hasOwner) headers.push("Owner");
  const rows = dbs.map(x => {
    const r = [x.name, fmtBytes(x.size_bytes)];
    if (hasTables) r.push(x.tables ?? 0);
    if (hasOwner) r.push(x.owner || "");
    return r;
  });
  return card({
    title, icon, entry,
    status: pill(up ? "up" : "down", up ? "ok" : "bad"),
    sub: d.version ? `version ${d.version}` : `${d.host}:${d.port}`,
    body: [
      st.note,
      kv([
        ["Address", `${d.host}:${d.port}`],
        ["Server port", d.server_port || ""],
        ["Uptime", d.uptime_seconds > 0 ? fmtDuration(d.uptime_seconds) : ""],
        ["Connections", up && d.version ? d.connections : ""],
        ["Databases", up && d.version ? dbs.length : ""],
      ]),
      d.query_error ? h("div", { class: "error" }, d.query_error) : null,
      !up ? h("div", { class: "muted" }, `Nothing is accepting connections on ${d.host}:${d.port}.`) : null,
      rows.length ? table(headers, rows, hasTables ? [1, 2] : [1]) : null,
    ],
  });
}

function renderIptables(entry) {
  const st = serviceState(entry);
  if (!entry || !entry.data) {
    return [card({ title: "iptables", icon: "firewall", status: st.status, body: st.note, entry })];
  }
  const d = entry.data;
  const multi = (d.families || []).length > 1;
  const policies = (d.policies || []).map(p => {
    const cls = p.policy === "ACCEPT" ? "ok" : p.policy === "DROP" ? "bad" : "warn";
    return h("span", { class: `pill ${cls}` }, `${multi ? p.family + " " : ""}${p.chain}: ${p.policy}`);
  });
  const ports = d.open_ports || [];
  const rules = d.rules || [];
  const headers = ["Port(s)", "Proto", "Target", "Chain", "Source", "Dest", "In"];
  if (multi) headers.unshift("Family");
  const rows = ports.map(p => {
    const target = p.target === "ACCEPT" ? pill("ACCEPT", "ok")
      : (p.target === "DROP" || p.target === "REJECT") ? pill(p.target, "bad")
      : pill(p.target || "—", "off");
    const r = [h("code", {}, p.ports), p.protocol, target, p.chain, p.source || "any", p.dest || "any", p.in_iface || "any"];
    if (multi) r.unshift(p.family);
    return r;
  });
  return [card({
    title: "iptables", icon: "firewall", entry, wide: true,
    status: pill(`${rules.length} rules`, entry.error ? "warn" : "ok"),
    sub: `filter table · ${(d.families || []).join(", ")}`,
    body: [
      st.note,
      h("div", { class: "muted small" }, "Chain policies"),
      h("div", { class: "chips" }, policies),
      h("div", { class: "muted small" }, "Port rules"),
      rows.length ? table(headers, rows) : h("div", { class: "muted small" }, "No port-specific rules"),
      rules.length ? h("details", {},
        h("summary", {}, `All rules (${rules.length})`),
        h("pre", {}, rules.map(r => `${multi ? r.family + " " : ""}-A ${r.chain} ${r.rule}`).join("\n"))) : null,
    ],
  })];
}

// ------------------------------------------------------------ overview

const SERVICES = [
  { key: "system", label: "System", icon: "server" },
  { key: "docker", label: "Docker", icon: "docker" },
  { key: "mysql", label: "MySQL", icon: "mysql" },
  { key: "postgres", label: "PostgreSQL", icon: "postgres" },
  { key: "iptables", label: "iptables", icon: "firewall" },
  { key: "systemd", label: "systemd", icon: "service" },
];

const SEVERITY = { ok: 0, off: 1, warn: 2, bad: 3 };

// One-line summary of a collector's data, shown in the overview.
function serviceDetail(key, d) {
  switch (key) {
    case "system": return `CPU ${d.cpu_percent}% · RAM ${d.memory.used_percent}% · up ${fmtDuration(d.uptime_seconds)}`;
    case "docker": return `${d.running}/${d.containers} containers running · v${d.server_version}`;
    case "mysql":
    case "postgres": return d.status === "up"
      ? `${d.host}:${d.port}${d.version ? ` · v${d.version}` : ""}${d.version ? ` · ${d.connections} conn.` : ""}`
      : `nothing listening on ${d.host}:${d.port}`;
    case "iptables": return `${(d.rules || []).length} rules · ${(d.open_ports || []).length} port rules`;
    case "systemd": return `${d.running} running · ${d.failed} failed${d.missing ? ` · ${d.missing} missing` : ""} · ${d.total} listed`;
  }
  return "";
}

// Health of a collector entry: {label, cls, detail}.
function serviceHealth(key, entry) {
  if (!entry || !entry.enabled) return { label: "disabled", cls: "off", detail: "Disabled in configuration" };
  if (!entry.data && entry.error) return { label: "error", cls: "bad", detail: entry.error };
  if (!entry.data) return { label: "pending", cls: "off", detail: "Waiting for first collection…" };
  const d = entry.data;
  const detail = serviceDetail(key, d);
  if ((key === "mysql" || key === "postgres") && d.status !== "up") return { label: "down", cls: "bad", detail };
  if (entry.error) return { label: "stale", cls: "warn", detail: entry.error };
  const age = (Date.now() - new Date(entry.updated_at).getTime()) / 1000;
  if (age > Math.max(3 * entry.interval_seconds, entry.interval_seconds + 30)) {
    return { label: "stale", cls: "warn", detail: `no update for ${fmtDuration(age)}` };
  }
  if (key === "systemd" && d.failed > 0) return { label: `${d.failed} failed`, cls: "bad", detail };
  if (key === "systemd" && d.missing > 0) return { label: `${d.missing} missing`, cls: "warn", detail };
  if ((key === "mysql" || key === "postgres") && d.query_error) return { label: "degraded", cls: "warn", detail: d.query_error };
  return { label: key === "mysql" || key === "postgres" ? "up" : "ok", cls: "ok", detail };
}

function renderOverview(sv) {
  const rows = SERVICES.map(s => ({ ...s, entry: sv[s.key], health: serviceHealth(s.key, sv[s.key]) }));
  const enabled = rows.filter(r => r.entry && r.entry.enabled);
  const healthy = enabled.filter(r => r.health.cls === "ok").length;
  const worst = enabled.reduce((w, r) => SEVERITY[r.health.cls] > SEVERITY[w] ? r.health.cls : w, "ok");
  return [card({
    title: "Monitored services", icon: "status", wide: true,
    status: pill(`${healthy}/${enabled.length} healthy`, worst === "off" ? "ok" : worst),
    sub: "Status of every collector",
    body: table(["Service", "Status", "Details", "Updated"], rows.map(r => [
      h("span", { class: "svc-name" }, h("img", { class: "row-icon", src: `icons/${r.icon}.svg`, alt: "" }), r.label),
      pill(r.health.label, r.health.cls),
      h("span", { class: r.health.cls === "bad" ? "error-text" : null, title: r.health.detail }, r.health.detail),
      r.entry && r.entry.enabled
        ? h("span", { title: fmtTime(r.entry.updated_at) }, r.entry.updated_at ? fmtAgo(r.entry.updated_at) : "never")
        : "—",
    ])),
  })];
}

// ------------------------------------------------------------ systemd

function unitPill(u) {
  if (u.load === "not-found") return pill("not found", "bad");
  if (u.load === "masked") return pill("masked", "off");
  if (u.active === "failed") return pill("failed", "bad");
  if (u.active === "active") return pill(u.sub === "running" ? "running" : u.sub, "ok");
  if (u.active === "inactive") return pill("stopped", "off");
  return pill(u.active, "warn"); // activating, deactivating, reloading…
}

function unitRank(u) {
  if (u.load === "not-found" || u.active === "failed") return 0;
  if (u.active !== "active" && u.active !== "inactive") return 1;
  if (u.active === "active") return u.sub === "running" ? 2 : 3;
  return 4;
}

function renderSystemd(entry) {
  const st = serviceState(entry);
  if (!entry || !entry.data) {
    return [card({ title: "systemd services", icon: "service", status: st.status, body: st.note, entry })];
  }
  const d = entry.data;
  const units = [...(d.units || [])].sort((a, b) => unitRank(a) - unitRank(b) || a.name.localeCompare(b.name));
  return [card({
    title: "systemd services", icon: "service", entry, wide: true,
    status: d.failed > 0 ? pill(`${d.failed} failed`, "bad")
      : d.missing > 0 ? pill(`${d.missing} missing`, "warn") : pill(entry.error ? "stale" : `${d.running} running`, entry.error ? "warn" : "ok"),
    sub: "Failed units first, then running, then the rest",
    body: [
      st.note,
      kv([["Running", d.running], ["Failed", d.failed], ["Inactive", d.inactive], ["Missing", d.missing || ""], ["Listed", d.total]]),
      units.length
        ? table(["Service", "Status", "Description"], units.map(u => [
            h("code", { title: `${u.name} · load: ${u.load} · active: ${u.active} · sub: ${u.sub}` }, u.name.replace(/\.service$/, "")),
            unitPill(u),
            u.description || "—",
          ]))
        : h("div", { class: "muted small" }, "No services matched"),
    ],
  })];
}

// ------------------------------------------------------------ main loop

function replace(id, nodes) {
  document.getElementById(id).replaceChildren(...nodes.filter(Boolean));
}

function render(s) {
  const sv = s.services || {};
  document.getElementById("hostname").textContent = s.hostname || "Server Admin";
  document.title = `${s.hostname || "Server"} · Admin`;
  document.getElementById("subtitle").textContent =
    `last refresh ${new Date().toLocaleTimeString()} · server time ${fmtTime(s.server_time)}`;

  replace("overview", renderOverview(sv));
  replace("system", renderSystem(sv.system));
  replace("services", renderSystemd(sv.systemd));
  replace("docker", renderDocker(sv.docker));
  replace("databases", [renderDB(sv.mysql, "MySQL", "mysql"), renderDB(sv.postgres, "PostgreSQL", "postgres")]);
  replace("firewall", renderIptables(sv.iptables));
}

async function refresh() {
  const banner = document.getElementById("banner");
  try {
    // Built from origin so it works even when the page URL embeds credentials.
    const url = new URL("api/status", location.origin + location.pathname);
    const res = await fetch(url, { cache: "no-store" });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    render(await res.json());
    banner.classList.add("hidden");
  } catch (e) {
    banner.textContent = `Cannot reach API: ${e.message}`;
    banner.classList.remove("hidden");
  }
}

function schedule() {
  clearInterval(timer);
  if (document.getElementById("autorefresh").checked) timer = setInterval(refresh, REFRESH_MS);
}

document.getElementById("refresh").addEventListener("click", refresh);
document.getElementById("autorefresh").addEventListener("change", schedule);
refresh();
schedule();
