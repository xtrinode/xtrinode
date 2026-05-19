const statusUrl = "/ui/admin/api/gateway/status";
const state = {
  data: null,
  rows: [],
  selectedKey: ""
};

const elements = {
  generated: document.getElementById("generated"),
  search: document.getElementById("search"),
  backendState: document.getElementById("state"),
  health: document.getElementById("health"),
  routeKind: document.getElementById("route-kind"),
  rows: document.getElementById("rows"),
  empty: document.getElementById("empty"),
  error: document.getElementById("error"),
  drawer: document.getElementById("backend-detail"),
  drawerBackdrop: document.getElementById("drawer-backdrop"),
  drawerClose: document.getElementById("drawer-close"),
  drawerTitle: document.getElementById("drawer-title"),
  drawerSubtitle: document.getElementById("drawer-subtitle"),
  drawerSelectors: document.getElementById("drawer-selectors"),
  drawerCoordinator: document.getElementById("drawer-coordinator"),
  drawerTrinoUI: document.getElementById("drawer-trino-ui"),
  drawerBasics: document.getElementById("drawer-basics"),
  drawerLifecycle: document.getElementById("drawer-lifecycle"),
  drawerHealth: document.getElementById("drawer-health"),
  drawerCircuit: document.getElementById("drawer-circuit"),
  drawerReload: document.getElementById("drawer-reload"),
  drawerExamples: document.getElementById("drawer-examples"),
  metrics: {
    routes: document.getElementById("metric-routes"),
    backends: document.getElementById("metric-backends"),
    running: document.getElementById("metric-running"),
    paused: document.getElementById("metric-paused"),
    unhealthy: document.getElementById("metric-unhealthy"),
    circuits: document.getElementById("metric-circuits")
  }
};

async function loadStatus() {
  elements.error.hidden = true;
  try {
    const response = await fetch(statusUrl, {
      headers: { "Accept": "application/json" },
      credentials: "same-origin",
      cache: "no-store"
    });
    if (!response.ok) {
      throw new Error(`Status API returned ${response.status}`);
    }
    state.data = await response.json();
    state.rows = flattenRows(state.data.routes || []);
    render();
  } catch (error) {
    elements.error.textContent = error.message;
    elements.error.hidden = false;
  }
}

function flattenRows(routes) {
  return routes.flatMap(route => (route.backends || []).map(backend => ({
    route,
    backend
  })));
}

function render() {
  const data = state.data || { summary: {}, reload: {} };
  const summary = data.summary || {};
  elements.metrics.routes.textContent = summary.routes || 0;
  elements.metrics.backends.textContent = summary.backends || 0;
  elements.metrics.running.textContent = summary.runningBackends || 0;
  elements.metrics.paused.textContent = summary.pausedBackends || 0;
  elements.metrics.unhealthy.textContent = (summary.unhealthyBackends || 0) + (summary.sleepingBackends || 0);
  elements.metrics.circuits.textContent = summary.openCircuitBackends || 0;

  elements.generated.textContent = data.generatedAt ? formatTime(data.generatedAt) : "";
  renderRows(filterRows(state.rows));
  refreshDrawer();
}

function filterRows(rows) {
  const query = elements.search.value.trim().toLowerCase();
  const wantedState = elements.backendState.value;
  const wantedHealth = elements.health.value;
  const routeKind = elements.routeKind.value;
  return rows.filter(({ route, backend }) => {
    if (wantedState && backend.state !== wantedState) return false;
    if (wantedHealth && (backend.health?.state || "unknown") !== wantedHealth) return false;
    if (routeKind === "default" && !route.default) return false;
    if (routeKind === "hostname" && !route.hostname) return false;
    if (routeKind === "header" && !route.header) return false;
    if (!query) return true;
    const haystack = [
      backend.name,
      backend.namespace,
      backend.coordinatorUrl,
      backend.state,
      route.name,
      route.routingGroup,
      route.hostname,
      route.header
    ].filter(Boolean).join(" ").toLowerCase();
    return haystack.includes(query);
  });
}

function renderRows(rows) {
  elements.rows.replaceChildren();
  elements.empty.hidden = rows.length !== 0;
  for (const row of rows) {
    const tr = document.createElement("tr");
    const key = rowKey(row);
    tr.tabIndex = 0;
    tr.setAttribute("role", "button");
    tr.setAttribute("aria-label", `Open ${row.backend.namespace}/${row.backend.name} details`);
    if (key === state.selectedKey) {
      tr.className = "selected";
    }
    tr.addEventListener("click", () => openDrawer(row));
    tr.addEventListener("keydown", event => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        openDrawer(row);
      }
    });
    tr.append(
      cell(row.backend.name, "mono"),
      cell(row.backend.namespace, "mono"),
      routeCell(row.route),
      badgeCell(row.backend.state || "RUNNING", stateClass(row.backend.state)),
      healthCell(row.backend),
      circuitCell(row.backend.circuitBreaker || {}),
      cell(row.backend.coordinatorUrl, "mono")
    );
    elements.rows.appendChild(tr);
  }
}

function rowKey(row) {
  return [
    row.route.name,
    row.route.routingGroup,
    row.backend.namespace,
    row.backend.name,
    row.backend.coordinatorUrl
  ].filter(Boolean).join("|");
}

function openDrawer(row) {
  state.selectedKey = rowKey(row);
  renderRows(filterRows(state.rows));
  renderDrawer(row);
  elements.drawer.hidden = false;
  elements.drawerBackdrop.hidden = false;
  elements.drawer.setAttribute("aria-hidden", "false");
  window.requestAnimationFrame(() => elements.drawer.classList.add("open"));
}

function closeDrawer() {
  state.selectedKey = "";
  elements.drawer.classList.remove("open");
  elements.drawer.setAttribute("aria-hidden", "true");
  elements.drawer.hidden = true;
  elements.drawerBackdrop.hidden = true;
  renderRows(filterRows(state.rows));
}

function refreshDrawer() {
  if (!state.selectedKey || elements.drawer.hidden) return;
  const row = state.rows.find(candidate => rowKey(candidate) === state.selectedKey);
  if (!row) {
    closeDrawer();
    return;
  }
  renderDrawer(row);
}

function renderDrawer(row) {
  const route = row.route;
  const backend = row.backend;
  const health = displayHealth(backend);
  const circuit = backend.circuitBreaker || { state: "closed" };
  const lifecycle = backend.lifecycle || {};
  const reload = state.data?.reload || {};

  elements.drawerTitle.textContent = backend.name || "unknown";
  elements.drawerSubtitle.textContent = `${backend.namespace || "default"} / ${route.name || route.routingGroup || "route"}`;
  elements.drawerCoordinator.textContent = backend.coordinatorUrl || "not reported";
  renderSelectors(route);
  renderTrinoUILink(backend);
  renderFieldList(elements.drawerBasics, [
    ["State", backend.state || "RUNNING"],
    ["Route active", backend.active ? "yes" : "no"],
    ["Selectable for new queries", selectabilityLabel(backend)],
    ["Tier", backend.tier || "not set"],
    ["Capacity units", backend.capacityUnits || "not set"],
    ["Drain until", backend.drainUntil || "not set"]
  ]);
  renderFieldList(elements.drawerLifecycle, [
    ["Auto suspend after", lifecycle.autoSuspendAfter || "not set"],
    ["Last activity", formatOptionalDate(lifecycle.lastActivity)],
    ["Suspend ETA", formatSuspendEta(lifecycle.suspendAt)]
  ]);
  renderFieldList(elements.drawerHealth, [
    ["State", health.state || "unknown"],
    ["Last status", health.lastStatus || "not reported"],
    ["Consecutive failures", health.consecutiveFailures || 0],
    ["Last check", formatOptionalDate(health.lastCheck)],
    ["Last success", formatOptionalDate(health.lastSuccess)],
    ["Last error", health.lastError || "none"]
  ]);
  renderFieldList(elements.drawerCircuit, [
    ["Known", circuit.known ? "yes" : "no"],
    ["State", circuit.state || "closed"],
    ["Consecutive failures", circuit.consecutiveFailures || 0],
    ["Consecutive successes", circuit.consecutiveSuccesses || 0],
    ["Consecutive overloads", circuit.consecutiveOverloads || 0],
    ["Last failure", formatOptionalDate(circuit.lastFailure)],
    ["Last success", formatOptionalDate(circuit.lastSuccess)]
  ]);
  renderFieldList(elements.drawerReload, [
    ["Last attempt", formatOptionalDate(reload.lastAttempt)],
    ["Last success", formatOptionalDate(reload.lastSuccess)],
    ["Last failure", formatOptionalDate(reload.lastFailure)],
    ["Resource version", reload.configMapResourceVersion || "not reported"],
    ["Routes loaded", reload.routesLoaded ?? 0],
    ["Invalid routes", reload.invalidRoutes ?? 0],
    ["Last error", reload.lastError || "none"]
  ]);
  renderExamples(row);
}

function renderTrinoUILink(backend) {
  elements.drawerTrinoUI.replaceChildren();
  if (!backend.trinoUiPath) {
    const empty = document.createElement("div");
    empty.className = "empty-inline";
    empty.textContent = "No Trino UI route is available for this backend.";
    elements.drawerTrinoUI.appendChild(empty);
    return;
  }
  const actions = document.createElement("div");
  actions.className = "link-actions";
  const link = document.createElement("a");
  link.className = "link-button";
  link.href = backend.trinoUiPath;
  link.textContent = "Open Trino UI";
  const code = document.createElement("code");
  code.className = "mono link-path";
  code.textContent = backend.trinoUiPath;
  actions.append(link, code);
  elements.drawerTrinoUI.appendChild(actions);
}

function renderSelectors(route) {
  elements.drawerSelectors.replaceChildren();
  const selectors = [
    ["Default", route.default ? "yes" : "no"],
    ["Routing group", route.routingGroup || "not set"],
    ["Hostname", route.hostname || "not set"],
    ["Header", route.header ? `X-Trino-XTrinode: ${route.header}` : "not set"]
  ];
  for (const [label, value] of selectors) {
    const pill = document.createElement("span");
    pill.className = "selector-pill mono";
    pill.textContent = `${label}: ${value}`;
    elements.drawerSelectors.appendChild(pill);
  }
}

function renderFieldList(container, rows) {
  const dl = document.createElement("dl");
  dl.className = "detail-list";
  for (const [label, value] of rows) {
    const dt = document.createElement("dt");
    dt.className = "detail-label";
    dt.textContent = label;
    const dd = document.createElement("dd");
    dd.className = "detail-value";
    dd.textContent = String(value);
    dl.append(dt, dd);
  }
  container.replaceChildren(dl);
}

function renderExamples(row) {
  elements.drawerExamples.replaceChildren();
  const examples = buildQueryExamples(row);
  if (examples.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "This route has no hostname, header, or default selector for direct gateway queries.";
    elements.drawerExamples.appendChild(empty);
    return;
  }

  if (state.data?.ui?.requireAuth) {
    const hint = document.createElement("div");
    hint.className = "hint";
    hint.textContent = "Gateway authentication is enabled. Copy examples omit Authorization and API key headers.";
    elements.drawerExamples.appendChild(hint);
  }

  for (const example of examples) {
    const article = document.createElement("article");
    article.className = "example";
    const head = document.createElement("div");
    head.className = "example-head";
    const title = document.createElement("div");
    title.className = "example-title";
    title.textContent = example.title;
    const button = document.createElement("button");
    button.className = "copy-button";
    button.type = "button";
    button.textContent = "Copy";
    button.addEventListener("click", () => copyText(example.command, button));
    head.append(title, button);
    const pre = document.createElement("pre");
    const code = document.createElement("code");
    code.textContent = example.command;
    pre.appendChild(code);
    article.append(head, pre);
    elements.drawerExamples.appendChild(article);
  }
}

function selectabilityLabel(backend) {
  const result = backendSelectability(backend);
  return result.selectable ? "yes" : `no - ${result.reason}`;
}

function backendSelectability(backend) {
  const backendState = backend.state || "RUNNING";
  if (backendState !== "RUNNING") {
    return { selectable: false, reason: backendState.toLowerCase() };
  }
  if (!backend.active) {
    return { selectable: false, reason: "route inactive" };
  }

  const healthState = backend.health?.state || "unknown";
  if (healthState === "sleeping" || healthState === "unhealthy") {
    return { selectable: false, reason: healthState };
  }

  const circuit = backend.circuitBreaker || {};
  if (circuit.known && circuit.state === "open") {
    return { selectable: false, reason: "open circuit" };
  }

  return { selectable: true, reason: "" };
}

function buildQueryExamples(row) {
  const selectors = routeQuerySelectors(row.route);
  if (selectors.length === 0) return [];
  const examples = selectors.map(selector => ({
    title: `${selector.label} smoke query`,
    command: buildCurlCommand(selector.headers, "SELECT 1")
  }));
  if (selectors.length === 1) {
    examples.push({
      title: `${selectors[0].label} catalog list`,
      command: buildCurlCommand(selectors[0].headers, "SHOW CATALOGS")
    });
  }
  return examples;
}

function routeQuerySelectors(route) {
  const selectors = [];
  if (route.hostname) {
    selectors.push({ label: "Hostname selector", headers: [["Host", route.hostname]] });
  }
  if (route.header) {
    selectors.push({ label: "Header selector", headers: [["X-Trino-XTrinode", route.header]] });
  }
  if (route.default) {
    selectors.push({ label: "Default route", headers: [] });
  }
  return selectors;
}

function buildCurlCommand(selectorHeaders, statement) {
  const headers = [["X-Trino-User", "local-ui"], ...selectorHeaders];
  const lines = [
    `curl -sS -X POST ${shellQuote(`${window.location.origin}/v1/statement`)}`
  ];
  for (const [name, value] of headers) {
    lines.push(`  -H ${shellQuote(`${name}: ${value}`)}`);
  }
  lines.push(`  --data ${shellQuote(statement)}`);
  return lines.join(" \\\n");
}

async function copyText(text, button) {
  const original = button.textContent;
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
    } else {
      fallbackCopy(text);
    }
    button.textContent = "Copied";
  } catch (error) {
    button.textContent = "Copy failed";
  } finally {
    window.setTimeout(() => {
      button.textContent = original;
    }, 1400);
  }
}

function fallbackCopy(text) {
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  try {
    textarea.select();
    if (!document.execCommand("copy")) {
      throw new Error("copy failed");
    }
  } finally {
    textarea.remove();
  }
}

function routeCell(route) {
  const td = document.createElement("td");
  const values = [];
  if (route.default) values.push("default");
  if (route.hostname) values.push("hostname");
  if (route.header) values.push("header");
  if (!values.length && route.routingGroup) values.push("group");
  td.textContent = values.join("\n") || route.name || "";
  td.className = "mono";
  td.title = routeSelectorDetails(route).join("\n");
  return td;
}

function routeSelectorDetails(route) {
  const details = [];
  if (route.default) details.push("default route");
  if (route.routingGroup) details.push(`group: ${route.routingGroup}`);
  if (route.hostname) details.push(`hostname: ${route.hostname}`);
  if (route.header) details.push(`header: ${route.header}`);
  return details;
}

function healthCell(backend) {
  const health = displayHealth(backend);
  const label = health.lastStatus ? `${health.state} ${health.lastStatus}` : health.state;
  return badgeCell(label, healthClass(health.classState || health.state));
}

function displayHealth(backend) {
  const health = backend.health || { state: "unknown" };
  if ((backend.state || "RUNNING") !== "RUNNING" && (health.state || "unknown") === "unknown") {
    return { ...health, state: "not checked", classState: "unknown" };
  }
  return { ...health, classState: health.state || "unknown" };
}

function circuitCell(circuit) {
  const label = circuit.known ? circuit.state : "closed";
  return badgeCell(label, circuit.state === "open" ? "red" : circuit.state === "half-open" ? "amber" : "green");
}

function cell(text, className = "") {
  const td = document.createElement("td");
  td.textContent = text || "";
  if (className) td.className = className;
  return td;
}

function badgeCell(text, color) {
  const td = document.createElement("td");
  const span = document.createElement("span");
  span.className = `pill ${color}`;
  span.textContent = text || "unknown";
  td.appendChild(span);
  return td;
}

function stateClass(value) {
  if (value === "RUNNING" || !value) return "green";
  if (value === "PAUSED" || value === "RESUMING" || value === "DRAINING") return "amber";
  return "gray";
}

function healthClass(value) {
  if (value === "healthy") return "green";
  if (value === "sleeping" || value === "unhealthy") return "red";
  return "gray";
}

function formatDate(value) {
  return new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    year: "numeric",
    month: "2-digit",
    day: "2-digit"
  }).format(new Date(value));
}

function formatOptionalDate(value) {
  return value ? formatDate(value) : "never";
}

function formatSuspendEta(value) {
  if (!value) return "not scheduled";
  const suspendAt = new Date(value);
  const generatedAt = state.data?.generatedAt ? new Date(state.data.generatedAt) : new Date();
  const remainingMillis = suspendAt.getTime() - generatedAt.getTime();
  if (!Number.isFinite(remainingMillis)) return formatDate(value);
  if (remainingMillis <= 0) return `${formatDate(value)} (due)`;
  return `${formatDate(value)} (${formatDuration(remainingMillis)} remaining)`;
}

function formatDuration(milliseconds) {
  let seconds = Math.ceil(milliseconds / 1000);
  const hours = Math.floor(seconds / 3600);
  seconds -= hours * 3600;
  const minutes = Math.floor(seconds / 60);
  seconds -= minutes * 60;
  const parts = [];
  if (hours) parts.push(`${hours}h`);
  if (minutes || hours) parts.push(`${minutes}m`);
  if (!hours && seconds) parts.push(`${seconds}s`);
  return parts.join(" ") || "0s";
}

function formatTime(value) {
  return new Intl.DateTimeFormat(undefined, {
    hour: "numeric",
    minute: "2-digit",
    second: "2-digit"
  }).format(new Date(value));
}

function shellQuote(value) {
  return `'${String(value).replaceAll("'", "'\"'\"'")}'`;
}

elements.drawerClose.addEventListener("click", closeDrawer);
elements.drawerBackdrop.addEventListener("click", closeDrawer);
window.addEventListener("keydown", event => {
  if (event.key === "Escape" && !elements.drawer.hidden) {
    closeDrawer();
  }
});
[elements.search, elements.backendState, elements.health, elements.routeKind].forEach(input => {
  input.addEventListener("input", render);
});

loadStatus();
window.setInterval(loadStatus, 10000);
