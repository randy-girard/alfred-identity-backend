const TABS = [
  { id: 'overview', label: 'Overview' },
  { id: 'accounts', label: 'Accounts' },
  { id: 'users', label: 'Users' },
  { id: 'groups', label: 'Groups' },
  { id: 'shares', label: 'Shared accounts' },
  { id: 'connections', label: 'Connections' },
  { id: 'sessions', label: 'Sessions' },
  { id: 'audit', label: 'Audit log' },
  { id: 'settings', label: 'Settings' },
]

const THEME_KEY = 'alfred-identity-theme'

function readStoredTheme() {
  try {
    const t = localStorage.getItem(THEME_KEY)
    return t === 'dark' ? 'dark' : 'light'
  } catch {
    return 'light'
  }
}

function applyTheme(theme) {
  const t = theme === 'dark' ? 'dark' : 'light'
  document.documentElement.setAttribute('data-theme', t)
  try {
    localStorage.setItem(THEME_KEY, t)
  } catch {
    /* ignore */
  }
  const btn = document.getElementById('theme-toggle')
  if (btn) btn.textContent = t === 'dark' ? 'Light' : 'Dark'
  return t
}

function toggleTheme() {
  applyTheme(readStoredTheme() === 'dark' ? 'light' : 'dark')
  if (tab === 'overview') {
    const main = $('#main')
    if (main) mountMetricCharts(main)
  }
}

let state = { accounts: [], shares: [], users: [], roles: [], groups: [], sessions: [], connections: [], online: [] }
let me = null
let tab = 'overview'
let busy = false
let ws = null
let connDurationTimer = null
let metricsRefreshTimer = null
let liveConnected = false
let metricsRange = '24h'
let metricsData = null
let metricChartInstances = []
let auditEntries = []
let auditOffset = 0
let auditLoading = false
let auditFilterAccount = 0
let auditFilterUser = 0
const AUDIT_PAGE = 100

/** @type {Record<string, {key: string, dir: number}>} */
const tableSort = {
  accounts: { key: 'username', dir: 1 },
  users: { key: 'name', dir: 1 },
  groups: { key: 'name', dir: 1 },
  shares: { key: 'username', dir: 1 },
  connections: { key: 'name', dir: 1 },
  sessions: { key: 'account', dir: 1 },
  audit: { key: 'created', dir: -1 },
}

/** Draft / edit state for the unified account modal */
let accountForm = null

const $ = (sel) => document.querySelector(sel)
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[c]))

function isWebAdmin() {
  return (me?.web_role || (me?.is_admin ? 'admin' : '')) === 'admin'
}

function myUserId() {
  return Number(me?.user_id || 0)
}

function isShareOwner(a) {
  return !!a?.restricted && Number(a.owner_user_id) === myUserId()
}

function canManageAccount(a) {
  if (!isWebAdmin()) return false
  if (a?.restricted) return isShareOwner(a)
  return true
}

function canManageShare(a) {
  return isWebAdmin() && isShareOwner(a)
}

function webRoleLabel(role) {
  if (role === 'admin') return 'Admin'
  if (role === 'readonly') return 'Read-only'
  return 'Off'
}

const DISCORD_COMMANDS = [
  { id: 'sso', label: 'SSO token commands (/sso)' },
  { id: 'whoami', label: 'Identity lookup (/whoami)' },
]

function discordCommandsLabel(cmds) {
  if (!cmds || !cmds.length) return '—'
  return cmds.map((c) => `/${c}`).join(', ')
}

function discordCommandsFieldHTML(selected = []) {
  const set = new Set((selected || []).map(String))
  return DISCORD_COMMANDS.map((c) => `
    <label class="role-item">
      <input type="checkbox" name="m-g-cmd" value="${c.id}" ${set.has(c.id) ? 'checked' : ''}/>
      <span>${esc(c.label)}</span>
    </label>`).join('')
}

function readDiscordCommandsFromModal(root) {
  return [...root.querySelectorAll('input[name=m-g-cmd]:checked')].map((i) => i.value)
}

function roleName(id) {
  const r = (state.roles || []).find((x) => x.id === id)
  return r?.name || id
}

function discordUserHTML(u) {
  if (!u) return '<span class="muted">—</span>'
  const name = u.display_name || u.discord_id || `#${u.id}`
  const did = u.discord_id || ''
  return `<div class="discord-user">
    <div class="discord-user-name">${esc(name)}</div>
    ${did ? `<div class="muted mono discord-user-id">${esc(did)}</div>` : ''}
  </div>`
}

function showError(msg) {
  const el = $('#banner')
  if (!msg) {
    el.classList.add('hidden')
    el.textContent = ''
    return
  }
  el.textContent = msg
  el.classList.remove('hidden')
}

async function api(path, opts = {}) {
  const headers = { ...(opts.headers || {}) }
  const isForm = typeof FormData !== 'undefined' && opts.body instanceof FormData
  if (!isForm && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json'
  }
  const res = await fetch(path, {
    credentials: 'same-origin',
    ...opts,
    headers,
  })
  if (res.status === 401) {
    window.location.href = '/admin/login'
    throw new Error('unauthorized')
  }
  const data = await res.json().catch(() => ({}))
  if (!res.ok || data.ok === false) {
    throw new Error(data.error || res.statusText || 'request failed')
  }
  return data
}

async function run(fn) {
  if (busy) return
  busy = true
  showError('')
  try {
    await fn()
    // state usually arrives via websocket; refresh as fallback
    await refreshState()
  } catch (e) {
    showError(String(e.message || e))
  } finally {
    busy = false
    render()
  }
}

async function refreshState(skipRender) {
  const data = await api('/admin/api/state')
  applyState(data, skipRender)
}

function applyState(data, skipRender) {
  state = {
    accounts: data.accounts || [],
    shares: data.shares || [],
    users: data.users || [],
    roles: data.roles || [],
    groups: data.groups || [],
    sessions: data.sessions || [],
    connections: data.connections || [],
    online: data.online || [],
  }
  if (skipRender) return
  if (tab === 'overview') {
    refreshOverviewLive()
    return
  }
  if (tab === 'connections') {
    const main = $('#main')
    if (main) main.innerHTML = renderConnections()
    return
  }
  if (tab === 'sessions') {
    const main = $('#main')
    if (main) main.innerHTML = renderSessions()
  }
}

function connectLive() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/admin/ws`)
  const status = $('#live-status')
  ws.onopen = () => {
    liveConnected = true
    status.textContent = 'Live · connected'
    status.className = 'ok'
    if (tab === 'overview') refreshOverviewLive()
  }
  ws.onclose = () => {
    liveConnected = false
    status.textContent = 'Live · disconnected (retrying…)'
    status.className = 'bad'
    if (tab === 'overview') refreshOverviewLive()
    setTimeout(connectLive, 2500)
  }
  ws.onerror = () => {}
  ws.onmessage = (ev) => {
    try {
      const msg = JSON.parse(ev.data)
      if (msg.type === 'state') applyState(msg)
    } catch (_) {}
  }
}

function closeModal() {
  $('#modal-root').innerHTML = ''
}

function modal(title, bodyHTML, onSubmit) {
  const root = $('#modal-root')
  root.innerHTML = `
    <div class="modal-backdrop" data-close="1">
      <div class="modal wide" role="dialog">
        <h2>${esc(title)}</h2>
        <div class="form-grid">${bodyHTML}</div>
        <div class="modal-actions">
          <button type="button" class="secondary" data-cancel="1">Cancel</button>
          <button type="button" data-save="1">Save</button>
        </div>
      </div>
    </div>`
  root.querySelector('[data-close]').addEventListener('click', (e) => {
    if (e.target.dataset.close) closeModal()
  })
  root.querySelector('[data-cancel]').addEventListener('click', closeModal)
  root.querySelector('[data-save]').addEventListener('click', () => onSubmit(root))
}

function renderTabs() {
  $('#tabs').innerHTML = TABS.map((t) => `
    <button type="button" class="tab ${tab === t.id ? 'active' : ''}" data-tab="${t.id}">${esc(t.label)}</button>
  `).join('')
  $('#tabs').querySelectorAll('[data-tab]').forEach((btn) => {
    btn.onclick = () => { tab = btn.dataset.tab; render() }
  })
}

function goTab(id) {
  tab = id
  render()
}

function sortRows(rows, tableId, getters) {
  const cfg = tableSort[tableId] || { key: '', dir: 1 }
  const get = getters[cfg.key]
  if (!get) return rows
  const dir = cfg.dir || 1
  return [...rows].sort((a, b) => {
    const va = get(a)
    const vb = get(b)
    if (typeof va === 'number' && typeof vb === 'number') return (va - vb) * dir
    return String(va ?? '').localeCompare(String(vb ?? ''), undefined, { sensitivity: 'base', numeric: true }) * dir
  })
}

function sortTh(tableId, key, label) {
  const cfg = tableSort[tableId] || {}
  const active = cfg.key === key
  const mark = active ? (cfg.dir > 0 ? ' ▲' : ' ▼') : ''
  return `<th class="sortable" data-sort-table="${esc(tableId)}" data-sort-key="${esc(key)}" title="Sort by ${esc(label)}">${esc(label)}${mark}</th>`
}

function bindSortable(root) {
  root.querySelectorAll('th.sortable').forEach((th) => {
    th.onclick = () => {
      const tableId = th.dataset.sortTable
      const key = th.dataset.sortKey
      if (!tableId || !key) return
      if (!tableSort[tableId]) tableSort[tableId] = { key, dir: 1 }
      if (tableSort[tableId].key === key) tableSort[tableId].dir *= -1
      else tableSort[tableId] = { key, dir: 1 }
      render()
    }
  })
}

function metric(label, value, tabId, hint) {
  const clickable = tabId ? ` data-goto="${tabId}" role="button" tabindex="0"` : ''
  return `<button type="button" class="metric"${clickable}${tabId ? '' : ' disabled'}>
    <span class="metric-value">${esc(String(value))}</span>
    <span class="metric-label">${esc(label)}</span>
    ${hint ? `<span class="metric-hint">${esc(hint)}</span>` : ''}
  </button>`
}

const METRIC_CHARTS = [
  {
    id: 'activity',
    title: 'Connections & sessions',
    hint: 'Desktop GUI WebSocket clients and in-game presence over the selected window.',
    series: [
      { key: 'gui_connections', label: 'GUI clients', color: 'accent' },
      { key: 'game_sessions', label: 'In-game sessions', color: 'ok' },
    ],
  },
  {
    id: 'db-latency',
    title: 'Database latency',
    hint: 'Round-trip time for a database ping on each sample.',
    series: [
      { key: 'db_latency_ms', label: 'Ping latency', unit: 'ms', color: 'warn' },
    ],
  },
  {
    id: 'db-pool',
    title: 'Database pool',
    hint: 'Open, in-use, and idle connections in the Go sql.DB pool.',
    series: [
      { key: 'db_in_use_connections', label: 'In use', color: 'accent' },
      { key: 'db_idle_connections', label: 'Idle', color: 'ok' },
      { key: 'db_open_connections', label: 'Open', color: 'muted' },
    ],
  },
]

const LIVE_METRIC_KEYS = {
  gui_connections: () => (state.connections || []).length,
  game_sessions: () => (state.sessions || []).length,
}

const DB_METRIC_KEYS = [
  'db_latency_ms',
  'db_open_connections',
  'db_in_use_connections',
  'db_idle_connections',
]

const METRIC_COLOR_FALLBACKS = {
  accent: '#0a84ff',
  ok: '#1a7f37',
  muted: '#666666',
  warn: '#c98200',
}

function chartLib() {
  return typeof Chart !== 'undefined' ? Chart : null
}

function readMetricsTheme() {
  const s = getComputedStyle(document.documentElement)
  return {
    accent: s.getPropertyValue('--accent').trim() || METRIC_COLOR_FALLBACKS.accent,
    ok: s.getPropertyValue('--ok').trim() || METRIC_COLOR_FALLBACKS.ok,
    muted: s.getPropertyValue('--muted').trim() || METRIC_COLOR_FALLBACKS.muted,
    warn: METRIC_COLOR_FALLBACKS.warn,
    fg: s.getPropertyValue('--fg').trim() || '#1c1c1c',
    panel: s.getPropertyValue('--panel').trim() || '#ffffff',
    line: s.getPropertyValue('--line').trim() || '#d0d0d0',
  }
}

function metricColor(name, theme) {
  return theme[name] || METRIC_COLOR_FALLBACKS[name] || theme.accent
}

function colorAlpha(color, alpha) {
  const c = String(color || '').trim()
  if (c.startsWith('#')) {
    const hex = c.slice(1)
    const full = hex.length === 3
      ? hex.split('').map((ch) => ch + ch).join('')
      : hex.padEnd(6, '0').slice(0, 6)
    const n = Number.parseInt(full, 16)
    if (!Number.isNaN(n)) {
      return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`
    }
  }
  return c
}

function parseMetricTime(iso) {
  if (!iso) return null
  const ms = Date.parse(iso)
  return Number.isNaN(ms) ? null : ms
}

function isCountMetric(key) {
  return key !== 'db_latency_ms'
}

function formatMetricValue(value, unit, key) {
  if (value == null || Number.isNaN(Number(value))) return '—'
  const n = Number(value)
  if (unit === 'ms' || key === 'db_latency_ms') {
    const text = n < 10 ? n.toFixed(1) : (Number.isInteger(n) ? String(n) : n.toFixed(1))
    return `${text} ms`
  }
  if (key && isCountMetric(key)) {
    return String(Math.round(n))
  }
  const text = Number.isInteger(n) ? String(n) : n.toFixed(1)
  return unit ? `${text} ${unit}` : text
}

function metricSeriesStats(points) {
  if (!points.length) return null
  const vals = points.map((p) => p.v)
  const sum = vals.reduce((a, b) => a + b, 0)
  return {
    min: Math.min(...vals),
    max: Math.max(...vals),
    avg: sum / vals.length,
    latest: vals[vals.length - 1],
  }
}

function metricSeriesStatsInt(points) {
  const st = metricSeriesStats(points)
  if (!st) return null
  return {
    min: Math.round(st.min),
    max: Math.round(st.max),
    avg: Math.round(st.avg),
    latest: Math.round(st.latest),
  }
}

function metricsTimeScaleOptions() {
  switch (metricsRange) {
    case '1h':
      return { unit: 'minute', displayFormats: { minute: 'HH:mm' } }
    case '24h':
      return { unit: 'hour', displayFormats: { hour: 'HH:mm', minute: 'HH:mm' } }
    case '7d':
      return { unit: 'day', displayFormats: { day: 'MMM d', hour: 'MMM d HH:mm' } }
    case '30d':
    case '90d':
      return { unit: 'day', displayFormats: { day: 'MMM d' } }
    default:
      return { unit: 'hour', displayFormats: { hour: 'HH:mm' } }
  }
}

function buildMetricDatasets(chartDef, theme) {
  return chartDef.series.map((cfg) => {
    let points = (metricsData?.series?.[cfg.key] || [])
      .map((p) => {
        let y = Number(p.v)
        if (isCountMetric(cfg.key)) y = Math.round(y)
        return { x: parseMetricTime(p.t), y }
      })
      .filter((p) => p.x != null && !Number.isNaN(p.y))
      .sort((a, b) => a.x - b.x)
    if (!points.length && metricsData?.current?.[cfg.key] != null) {
      let value = Number(metricsData.current[cfg.key])
      if (isCountMetric(cfg.key)) value = Math.round(value)
      if (!Number.isNaN(value)) points = [{ x: Date.now(), y: value }]
    }
    const stroke = metricColor(cfg.color, theme)
    return {
      label: cfg.label,
      data: points,
      borderColor: stroke,
      backgroundColor: colorAlpha(stroke, chartDef.series.length === 1 ? 0.18 : 0.08),
      tension: 0.3,
      pointRadius: 0,
      pointHoverRadius: 4,
      pointHitRadius: 12,
      borderWidth: 2,
      fill: chartDef.series.length === 1,
    }
  })
}

function metricsChartOptions(chartDef, theme) {
  const unit = chartDef.series.find((s) => s.unit)?.unit || ''
  const isLatency = chartDef.id === 'db-latency'
  const isCountChart = !isLatency
  const timeOpts = metricsTimeScaleOptions()
  return {
    responsive: true,
    maintainAspectRatio: false,
    animation: { duration: 350 },
    interaction: { mode: 'index', intersect: false },
    plugins: {
      legend: { display: false },
      tooltip: {
        backgroundColor: theme.panel,
        titleColor: theme.fg,
        bodyColor: theme.fg,
        borderColor: theme.line,
        borderWidth: 1,
        padding: 10,
        callbacks: {
          label(ctx) {
            const seriesCfg = chartDef.series[ctx.datasetIndex]
            return `${ctx.dataset.label}: ${formatMetricValue(ctx.parsed.y, seriesCfg?.unit || unit, seriesCfg?.key)}`
          },
        },
      },
    },
    scales: {
      x: {
        type: 'time',
        bounds: 'data',
        time: {
          tooltipFormat: 'PPpp',
          ...timeOpts,
        },
        grid: { color: colorAlpha(theme.line, 0.45) },
        border: { color: colorAlpha(theme.line, 0.8) },
        ticks: {
          color: theme.muted,
          maxRotation: 0,
          autoSkip: true,
          maxTicksLimit: 4,
        },
      },
      y: {
        beginAtZero: true,
        grace: isLatency ? '25%' : '8%',
        grid: { color: colorAlpha(theme.line, 0.45) },
        border: { color: colorAlpha(theme.line, 0.8) },
        ticks: {
          color: theme.muted,
          maxTicksLimit: 4,
          precision: isCountChart ? 0 : undefined,
          stepSize: isCountChart ? 1 : undefined,
          callback: (v) => formatMetricValue(v, unit, isCountChart ? 'count' : 'db_latency_ms'),
        },
        afterDataLimits(axis) {
          if (isLatency && axis.max <= 0) axis.max = 1
        },
      },
    },
  }
}

function destroyMetricCharts() {
  metricChartInstances.forEach((chart) => {
    try { chart.destroy() } catch { /* ignore */ }
  })
  metricChartInstances = []
  const ChartCtor = chartLib()
  if (!ChartCtor) return
  METRIC_CHARTS.forEach((def) => {
    const el = document.getElementById(`metrics-chart-${def.id}`)
    if (!el) return
    const existing = ChartCtor.getChart(el)
    if (existing) existing.destroy()
  })
}

function mountMetricCharts(root) {
  const ChartCtor = chartLib()
  if (!ChartCtor || !root || !metricsData || metricsData.error) {
    destroyMetricCharts()
    return
  }
  destroyMetricCharts()
  const theme = readMetricsTheme()
  METRIC_CHARTS.forEach((chartDef) => {
    const canvas = root.querySelector(`#metrics-chart-${chartDef.id}`)
    if (!canvas) return
    const datasets = buildMetricDatasets(chartDef, theme)
    if (!datasets.some((d) => d.data.length)) return
    const chart = new ChartCtor(canvas, {
      type: 'line',
      data: { datasets },
      options: metricsChartOptions(chartDef, theme),
    })
    metricChartInstances.push(chart)
  })
}

function syncMetricsCurrentFromState() {
  if (!metricsData || metricsData.error) return
  metricsData.current = {
    ...(metricsData.current || {}),
    gui_connections: LIVE_METRIC_KEYS.gui_connections(),
    game_sessions: LIVE_METRIC_KEYS.game_sessions(),
  }
}

function patchLiveMetricSeries() {
  if (!metricsData || metricsData.error) return
  if (!metricsData.series) metricsData.series = {}
  const now = Date.now()
  Object.entries(LIVE_METRIC_KEYS).forEach(([key, getter]) => {
    upsertMetricSample(key, getter(), now)
  })
}

function patchDbMetricsFromCurrent() {
  if (!metricsData || metricsData.error || !metricsData.current) return
  if (!metricsData.series) metricsData.series = {}
  const now = Date.now()
  DB_METRIC_KEYS.forEach((key) => {
    const value = metricsData.current[key]
    if (value == null || Number.isNaN(Number(value))) return
    upsertMetricSample(key, Number(value), now)
  })
}

function upsertMetricSample(key, value, nowMs) {
  if (isCountMetric(key)) value = Math.round(value)
  const series = metricsData.series[key] || (metricsData.series[key] = [])
  const last = series[series.length - 1]
  const lastTs = last ? parseMetricTime(last.t) : null
  if (last && lastTs != null && nowMs - lastTs < 120000) {
    last.v = value
    last.t = new Date(nowMs).toISOString()
  } else {
    series.push({ t: new Date(nowMs).toISOString(), v: value })
  }
}

function renderChartLegendHTML(chartDef) {
  const cur = metricsData?.current || {}
  const theme = readMetricsTheme()
  return chartDef.series.map((cfg) => {
    const points = metricsData?.series?.[cfg.key] || []
    const latest = points.length ? points[points.length - 1].v : cur[cfg.key]
    return `<span class="chart-legend-item">
      <span class="chart-legend-swatch" style="background:${esc(metricColor(cfg.color, theme))}"></span>
      <span>${esc(cfg.label)}</span>
      <strong>${esc(formatMetricValue(latest, cfg.unit || '', cfg.key))}</strong>
    </span>`
  }).join('')
}

function updateMetricChartsData(animate) {
  const ChartCtor = chartLib()
  if (!ChartCtor || !metricsData || metricsData.error) return
  const theme = readMetricsTheme()
  const mode = animate ? 'default' : 'none'
  METRIC_CHARTS.forEach((chartDef) => {
    const canvas = document.getElementById(`metrics-chart-${chartDef.id}`)
    if (!canvas) return
    let chart = ChartCtor.getChart(canvas)
    const datasets = buildMetricDatasets(chartDef, theme)
    if (!datasets.some((d) => d.data.length)) return
    if (!chart) {
      chart = new ChartCtor(canvas, {
        type: 'line',
        data: { datasets },
        options: metricsChartOptions(chartDef, theme),
      })
      metricChartInstances.push(chart)
      return
    }
    chart.data.datasets = datasets
    chart.options = metricsChartOptions(chartDef, theme)
    chart.update(mode)
    const article = document.querySelector(`.time-chart[data-chart="${chartDef.id}"]`)
    const legend = article?.querySelector('.chart-legend')
    if (legend) legend.innerHTML = renderChartLegendHTML(chartDef)
  })
}

function overviewStats() {
  const accounts = state.accounts || []
  const users = state.users || []
  const sessions = state.sessions || []
  const connections = state.connections || []
  const disabled = accounts.filter((a) => a.disabled).length
  const shares = state.shares || []
  const restricted = shares.filter((a) => !a.disabled).length
  const elevated = accounts.filter((a) => !a.disabled && !a.restricted && (
    (a.required_role_ids && a.required_role_ids.length) || a.required_role_id
    || (a.required_user_ids && a.required_user_ids.length) || a.required_user_id
    || (a.group_ids && a.group_ids.length)
  )).length
  const base = accounts.filter((a) => !a.disabled && !a.restricted
    && !(a.required_role_ids && a.required_role_ids.length) && !a.required_role_id
    && !(a.required_user_ids && a.required_user_ids.length) && !a.required_user_id
    && !(a.group_ids && a.group_ids.length)).length
  const revoked = users.filter((u) => u.access_revoked).length
  const withToken = users.filter((u) => u.has_active_token && !u.access_revoked).length
  const adminsOnline = connections.filter((c) => c.is_admin).length
  return {
    accounts, users, sessions, connections, disabled, restricted, elevated, base,
    revoked, withToken, adminsOnline,
  }
}

function renderOverviewMetricCards() {
  const s = overviewStats()
  return `
    ${metric('EQ accounts', s.accounts.length, 'accounts', `${s.base} all · ${s.elevated} limited · ${s.restricted} shared · ${s.disabled} disabled`)}
    ${metric('SSO users', s.users.length, 'users', `${s.withToken} with token · ${s.revoked} revoked`)}
    ${metric('Groups', (state.groups || []).length, 'groups')}
    ${metric('Private shares', s.restricted, 'shares')}
    ${metric('GUI connected', s.connections.length, 'connections', s.adminsOnline ? `${s.adminsOnline} admin` : 'desktop clients')}
    ${metric('Online in-game', s.sessions.length, 'sessions', 'presence heartbeats')}`
}

function renderOverviewOnlineRows() {
  const { accounts, users, sessions } = overviewStats()
  return sessions.slice(0, 8).map((sess) => {
    const acct = accounts.find((a) => a.id === sess.account_id)
    const u = users.find((x) => x.id === sess.user_id)
    return `<tr>
      <td class="mono">${esc(acct?.username || `#${sess.account_id}`)}</td>
      <td>${esc(sess.character_name || '—')}</td>
      <td>${esc(u?.display_name || u?.discord_id || (sess.user_id ? `#${sess.user_id}` : '—'))}</td>
    </tr>`
  }).join('')
}

function renderOverviewConnectionRows() {
  const { connections } = overviewStats()
  return [...connections]
    .sort((a, b) => String(a.connected_at || '').localeCompare(String(b.connected_at || '')))
    .slice(0, 8)
    .map((c) => `<tr>
      <td>${esc(c.display_name || c.discord_id || `#${c.user_id}`)}${c.is_admin ? ' <span class="muted">admin</span>' : ''}</td>
      <td class="mono">${esc(c.client_version || '—')}</td>
      <td class="conn-duration" data-connected-at="${esc(c.connected_at || '')}">${esc(formatDuration(c.connected_at))}</td>
    </tr>`).join('')
}

function patchOverviewPanels(main) {
  if (!main) return
  const liveLabel = liveConnected ? 'Live updates connected' : 'Live updates disconnected'
  const badge = main.querySelector('#overview-live-badge')
  if (badge) {
    badge.textContent = liveLabel
    badge.className = liveConnected ? 'ok' : 'bad'
  }
  const metricsEl = main.querySelector('#overview-metric-cards')
  if (metricsEl) metricsEl.innerHTML = renderOverviewMetricCards()
  const onlineBody = main.querySelector('#overview-online-body')
  if (onlineBody) {
    onlineBody.innerHTML = renderOverviewOnlineRows()
      || '<tr><td colspan="3" class="empty">Nobody online.</td></tr>'
  }
  const connBody = main.querySelector('#overview-connections-body')
  if (connBody) {
    connBody.innerHTML = renderOverviewConnectionRows()
      || '<tr><td colspan="3" class="empty">No GUI clients connected.</td></tr>'
  }
}

function refreshOverviewLive() {
  const main = $('#main')
  if (!main || tab !== 'overview') return
  bootstrapMetricsData()
  if (metricsData && !metricsData.error) {
    patchLiveMetricSeries()
    patchDbMetricsFromCurrent()
  }
  if (main.querySelector('#overview-metric-cards')) {
    patchOverviewPanels(main)
    if (metricsData && !metricsData.error) updateMetricChartsData(false)
    return
  }
  main.innerHTML = renderOverview()
  bindOverview(main)
}

function renderTimeSeriesChart(chartDef) {
  const series = chartDef.series.map((cfg) => {
    const points = (metricsData?.series?.[cfg.key] || [])
      .map((p) => ({ t: parseMetricTime(p.t), v: Number(p.v) }))
      .filter((p) => p.t != null && !Number.isNaN(p.v))
    return { ...cfg, points }
  })
  const allPoints = series.flatMap((s) => s.points)
  const legend = renderChartLegendHTML(chartDef)

  const summaryParts = series.map((s) => {
    const st = isCountMetric(s.key) ? metricSeriesStatsInt(s.points) : metricSeriesStats(s.points)
    if (!st) {
      const curVal = metricsData?.current?.[s.key]
      if (curVal == null || Number.isNaN(Number(curVal))) return ''
      return `${s.label}: now ${formatMetricValue(curVal, s.unit || '', s.key)}`
    }
    return `${s.label}: peak ${formatMetricValue(st.max, s.unit || '', s.key)}, avg ${formatMetricValue(st.avg, s.unit || '', s.key)}`
  }).filter(Boolean)
  const summary = summaryParts.length
    ? `<div class="chart-summary muted">${esc(summaryParts.join(' · '))}</div>`
    : ''

  const hasData = allPoints.length || chartDef.series.some((cfg) => {
    const curVal = metricsData?.current?.[cfg.key]
    return curVal != null && !Number.isNaN(Number(curVal))
  })
  const body = hasData
    ? `<div class="time-chart-wrap"><canvas id="metrics-chart-${esc(chartDef.id)}" aria-label="${esc(chartDef.title)} over time"></canvas></div>`
    : `<p class="muted empty chart-empty">No samples yet.</p>`

  return `<article class="time-chart" data-chart="${esc(chartDef.id)}" title="${esc(chartDef.hint || '')}">
    <div class="time-chart-head">
      <h3>${esc(chartDef.title)}</h3>
      <div class="chart-legend">${legend}</div>
    </div>
    ${body}
    ${summary}
  </article>`
}

function bootstrapMetricsData() {
  if (!metricsData || metricsData.error) {
    metricsData = {
      ok: true,
      range: metricsRange,
      series: {},
      current: {},
    }
  }
  if (!metricsData.series) metricsData.series = {}
  syncMetricsCurrentFromState()
  Object.entries(LIVE_METRIC_KEYS).forEach(([key, getter]) => {
    if (!metricsData.series[key]?.length) {
      upsertMetricSample(key, getter(), Date.now())
    }
  })
}

function renderMetricsSection() {
  bootstrapMetricsData()
  const err = metricsData?.error
  const charts = METRIC_CHARTS.map((cfg) => renderTimeSeriesChart(cfg)).join('')
  return `
    <section class="panel overview-panel metrics-panel">
      <div class="row head">
        <h2>Performance over time</h2>
        <label class="metrics-range">
          <span class="muted">Timeframe</span>
          <select id="metrics-range">
            <option value="1h"${metricsRange === '1h' ? ' selected' : ''}>Last hour</option>
            <option value="24h"${metricsRange === '24h' ? ' selected' : ''}>Last 24 hours</option>
            <option value="7d"${metricsRange === '7d' ? ' selected' : ''}>Last 7 days</option>
            <option value="30d"${metricsRange === '30d' ? ' selected' : ''}>Last 30 days</option>
            <option value="90d"${metricsRange === '90d' ? ' selected' : ''}>Last 90 days</option>
          </select>
        </label>
      </div>
      <p class="hint">Connections and sessions update live. Database metrics refresh about once per minute.</p>
      ${err ? `<p class="bad">${esc(err)}</p>` : ''}
      <div class="metrics-charts">${charts}</div>
    </section>`
}

async function loadMetrics(options = {}) {
  const { updateOverview = tab === 'overview' } = options
  try {
    metricsData = await api(`/admin/api/metrics?range=${encodeURIComponent(metricsRange)}`)
  } catch (e) {
    metricsData = { error: String(e.message || e), series: {}, current: {} }
    bootstrapMetricsData()
  }
  syncMetricsCurrentFromState()
  patchDbMetricsFromCurrent()
  if (!updateOverview) return
  const main = $('#main')
  if (!main || tab !== 'overview') return
  if (main.querySelector('#overview-metric-cards')) {
    patchOverviewPanels(main)
    updateMetricChartsData(true)
    return
  }
  destroyMetricCharts()
  main.innerHTML = renderOverview()
  bindOverview(main)
}

function renderOverview() {
  const liveLabel = liveConnected ? 'Live updates connected' : 'Live updates disconnected'
  const liveClass = liveConnected ? 'ok' : 'bad'

  return `
    <section class="panel overview-panel">
      <div class="row head">
        <h2>Overview</h2>
        <span id="overview-live-badge" class="${liveClass}">${esc(liveLabel)}</span>
      </div>
      <p class="hint">High-level status across SSO accounts, users, private shares, and live clients. Click a metric to open that tab.</p>
      <div id="overview-metric-cards" class="metrics">
        ${renderOverviewMetricCards()}
      </div>
    </section>
    ${renderMetricsSection()}
    <div class="overview-grid">
      <section class="panel">
        <div class="row head">
          <h2>Online now</h2>
          <button type="button" class="secondary" data-goto="sessions">All sessions</button>
        </div>
        <div class="table-wrap">
          <table class="data-table">
            <thead><tr><th>Account</th><th>Character</th><th>User</th></tr></thead>
            <tbody id="overview-online-body">${renderOverviewOnlineRows() || '<tr><td colspan="3" class="empty">Nobody online.</td></tr>'}</tbody>
          </table>
        </div>
      </section>
      <section class="panel">
        <div class="row head">
          <h2>Desktop clients</h2>
          <button type="button" class="secondary" data-goto="connections">All connections</button>
        </div>
        <div class="table-wrap">
          <table class="data-table">
            <thead><tr><th>User</th><th>Version</th><th>Connected</th></tr></thead>
            <tbody id="overview-connections-body">${renderOverviewConnectionRows() || '<tr><td colspan="3" class="empty">No GUI clients connected.</td></tr>'}</tbody>
          </table>
        </div>
      </section>
    </div>`
}

function refreshOverviewDurations() {
  document.querySelectorAll('.conn-duration[data-connected-at]').forEach((el) => {
    el.textContent = formatDuration(el.dataset.connectedAt)
  })
}

function bindOverview(root) {
  root.querySelectorAll('[data-goto]').forEach((el) => {
    const go = () => goTab(el.dataset.goto)
    el.addEventListener('click', go)
    el.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        go()
      }
    })
  })
  mountMetricCharts(root)
}

function renderAccounts() {
  const list = sortRows(state.accounts || [], 'accounts', {
    username: (a) => a.username || '',
    aliases: (a) => (a.aliases || []).filter((x) => !a.username || x.toLowerCase() !== a.username.toLowerCase()).join(', '),
    tags: (a) => (a.tags || []).join(', '),
    characters: (a) => (a.characters || []).join(', '),
    groups: (a) => groupNamesForAccount(a).join(', '),
    access: (a) => accountAccessLabel(a).replace(/<[^>]+>/g, ''),
  })
  const rows = list.map((a) => {
    const access = accountAccessLabel(a)
    const aliases = (a.aliases || []).filter((x) => !a.username || x.toLowerCase() !== a.username.toLowerCase())
    const groups = groupNamesForAccount(a)
    return `<tr>
      <td class="mono">${esc(a.username || '—')}</td>
      <td>${esc(aliases.join(', ') || '—')}</td>
      <td>${esc((a.tags || []).join(', ') || '—')}</td>
      <td>${esc((a.characters || []).join(', ') || '—')}</td>
      <td>${esc(groups.join(', ') || '—')}</td>
      <td>${access}</td>
      <td class="col-actions">
        ${canManageAccount(a)
    ? `<button type="button" class="secondary" data-edit="${a.id}">Edit</button>`
    : (a.restricted ? '<span class="muted">shared</span>' : '<button type="button" class="secondary" disabled title="Read-only access">Edit</button>')}
      </td>
    </tr>`
  }).join('')

  return `
    <section class="panel">
      <div class="row head">
        <h2>EQ accounts</h2>
        <div class="actions">
          <button type="button" class="secondary" id="import-csv" ${isWebAdmin() ? '' : 'disabled title="Read-only access"'}>Import CSV</button>
          <button type="button" class="secondary" id="export-csv" ${isWebAdmin() ? '' : 'disabled title="Read-only access"'}>Export CSV</button>
          <input type="file" id="csv-file" accept=".csv,text/csv" hidden/>
          <button type="button" id="add-account" ${isWebAdmin() ? '' : 'disabled title="Read-only access"'}>Add account</button>
        </div>
      </div>
      <p class="hint">
        Use <strong>Edit</strong> to manage password, access (roles, users, groups), aliases, tags, and characters.
        Empty access grants mean <strong>all</strong> SSO users.
        Private shares you have access to appear here read-only; owners manage them from the desktop GUI or <strong>Shared accounts</strong>.
      </p>
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr>
            ${sortTh('accounts', 'username', 'Account')}
            ${sortTh('accounts', 'aliases', 'Aliases')}
            ${sortTh('accounts', 'tags', 'Tags')}
            ${sortTh('accounts', 'characters', 'Characters')}
            ${sortTh('accounts', 'groups', 'Groups')}
            ${sortTh('accounts', 'access', 'Access')}
            <th class="col-actions"></th>
          </tr></thead>
          <tbody>${rows || '<tr><td colspan="7" class="empty">No accounts yet.</td></tr>'}</tbody>
        </table>
      </div>
    </section>`
}

function renderUsers() {
  const list = sortRows(state.users || [], 'users', {
    name: (u) => u.display_name || u.discord_id || '',
    roles: (u) => (u.role_ids || []).map(roleName).join(', '),
    groups: (u) => groupsForUser(u.id).map((g) => g.name).join(', '),
    token: (u) => (u.has_active_token ? '1' : '0'),
    access: (u) => (u.access_revoked ? 'revoked' : 'allowed'),
  })
  const rows = list.map((u) => {
    const roles = (u.role_ids || []).map(roleName).join(', ') || '—'
    const groups = groupsForUser(u.id)
    return `<tr>
      <td>${discordUserHTML(u)}</td>
      <td>${esc(roles)}</td>
      <td>${esc(groups.map((g) => g.name).join(', ') || '—')}</td>
      <td>${u.has_active_token ? 'active' : 'none'}</td>
      <td>${u.access_revoked ? 'revoked' : 'allowed'}</td>
      <td class="col-actions">
        <button type="button" class="secondary" data-edit-user="${u.id}" ${isWebAdmin() ? '' : 'disabled title="Read-only access"'}>Edit</button>
      </td>
    </tr>`
  }).join('')
  return `
    <section class="panel">
      <h2>Users</h2>
      <p class="hint">
        Discord users with an SSO token. Roles sync from Discord (read-only).
        Use <strong>Edit</strong> to manage group membership and revoke/restore SSO access.
      </p>
      <div class="table-wrap">
        <table class="data-table users-table">
          <colgroup>
            <col/><col/><col/><col/><col/><col class="w-actions"/>
          </colgroup>
          <thead><tr>
            ${sortTh('users', 'name', 'Discord user')}
            ${sortTh('users', 'roles', 'Roles')}
            ${sortTh('users', 'groups', 'Groups')}
            ${sortTh('users', 'token', 'Token')}
            ${sortTh('users', 'access', 'Access')}
            <th class="col-actions"></th>
          </tr></thead>
          <tbody>${rows || '<tr><td colspan="6" class="empty">No users yet.</td></tr>'}</tbody>
        </table>
      </div>
    </section>`
}

function renderSessions() {
  const list = sortRows(state.sessions || [], 'sessions', {
    account: (s) => {
      const acct = state.accounts.find((a) => a.id === s.account_id)
      return acct?.username || String(s.account_id)
    },
    character: (s) => s.character_name || '',
    user: (s) => {
      const u = (state.users || []).find((x) => x.id === s.user_id)
      return u?.display_name || u?.discord_id || String(s.user_id || '')
    },
    last_seen: (s) => s.last_seen || '',
  })
  const rows = list.map((s) => {
    const acct = state.accounts.find((a) => a.id === s.account_id)
    const u = (state.users || []).find((x) => x.id === s.user_id)
    return `<tr>
      <td class="mono">${esc(acct?.username || `#${s.account_id}`)}</td>
      <td>${esc(s.character_name || '—')}</td>
      <td>${discordUserHTML(u || (s.user_id ? { id: s.user_id, display_name: `User #${s.user_id}` } : null))}</td>
      <td class="mono">${esc(s.last_seen || '—')}</td>
    </tr>`
  }).join('')
  return `
    <section class="panel">
      <h2>Live sessions</h2>
      <p class="hint">Accounts currently marked online via GUI heartbeats / presence.</p>
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr>
            ${sortTh('sessions', 'account', 'Account')}
            ${sortTh('sessions', 'character', 'Character')}
            ${sortTh('sessions', 'user', 'Discord user')}
            ${sortTh('sessions', 'last_seen', 'Last seen')}
          </tr></thead>
          <tbody>${rows || '<tr><td colspan="4" class="empty">No active sessions.</td></tr>'}</tbody>
        </table>
      </div>
    </section>`
}

function formatDuration(iso) {
  if (!iso) return '—'
  const start = Date.parse(iso)
  if (Number.isNaN(start)) return '—'
  let sec = Math.max(0, Math.floor((Date.now() - start) / 1000))
  const h = Math.floor(sec / 3600)
  sec %= 3600
  const m = Math.floor(sec / 60)
  const s = sec % 60
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

function renderConnections() {
  const raw = [...(state.connections || [])]
  const list = sortRows(raw, 'connections', {
    name: (c) => c.display_name || c.discord_id || '',
    version: (c) => c.client_version || '',
    connected: (c) => c.connected_at || '',
    since: (c) => c.connected_at || '',
  })
  const rows = list.map((c) => `<tr>
      <td>${discordUserHTML(c)}${c.is_admin ? ' <span class="muted">(admin)</span>' : ''}</td>
      <td class="mono">${esc(c.client_version || '—')}</td>
      <td>${esc(formatDuration(c.connected_at))}</td>
      <td class="mono muted">${esc(c.connected_at || '—')}</td>
    </tr>`).join('')
  return `
    <section class="panel">
      <h2>Connections</h2>
      <p class="hint">Desktop GUI clients currently authenticated to the SSO WebSocket.</p>
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr>
            ${sortTh('connections', 'name', 'Discord user')}
            ${sortTh('connections', 'version', 'GUI version')}
            ${sortTh('connections', 'connected', 'Connected for')}
            ${sortTh('connections', 'since', 'Since')}
          </tr></thead>
          <tbody>${rows || '<tr><td colspan="4" class="empty">No GUI clients connected.</td></tr>'}</tbody>
        </table>
      </div>
    </section>`
}

function chipRowHTML(items, listKey) {
  if (!items.length) return `<span class="empty">None yet.</span>`
  return items.map((v) => `
    <span class="chip manage-chip">
      <code>${esc(v)}</code>
      <button type="button" class="chip-remove" data-rm-list="${esc(listKey)}" data-rm-val="${esc(v)}" title="Remove">×</button>
    </span>`).join('')
}

function renderAccountModalBody() {
  const f = accountForm
  if (!f) return ''
  const isEdit = !!f.id
  return `
    ${isEdit ? `<p class="hint">Editing <strong class="mono">${esc(f.username || '#' + f.id)}</strong></p>` : `
      <div><label>Account name</label><input id="m-user" value="${esc(f.username)}" autocomplete="off" autocapitalize="off"/></div>
    `}
    <div><label>${isEdit ? 'New password' : 'Password'}</label>
      <input id="m-pass" type="password" value="" placeholder="${isEdit ? 'Leave blank to keep' : ''}" autocomplete="off"/>
    </div>
    ${accountForm.restricted
    ? '<p class="hint form-span">Access for this private share (users, roles, groups) is managed in the desktop GUI (<strong>Local → Share</strong>).</p>'
    : accessFieldsHTML({
      required_role_ids: f.required_role_ids,
      required_user_ids: f.required_user_ids,
      group_ids: f.group_ids,
    })}
    ${isEdit ? `<label class="role-item"><input id="m-dis" type="checkbox" ${f.disabled ? 'checked' : ''}/><span>Disabled</span></label>` : ''}
    <div class="form-span">
      <label>Aliases</label>
      <div class="chip-row" id="m-aliases">${chipRowHTML(f.aliases, 'aliases')}</div>
      <div class="row" style="margin-top:0.35rem">
        <input id="m-alias-val" placeholder="Add alias" autocapitalize="off" autocomplete="off"/>
        <button type="button" class="secondary" data-add-list="aliases">Add</button>
      </div>
    </div>
    <div class="form-span">
      <label>Tags</label>
      <div class="chip-row" id="m-tags">${chipRowHTML(f.tags, 'tags')}</div>
      <div class="row" style="margin-top:0.35rem">
        <input id="m-tag-val" placeholder="Add tag" autocapitalize="off" autocomplete="off"/>
        <button type="button" class="secondary" data-add-list="tags">Add</button>
      </div>
    </div>
    <div class="form-span">
      <label>Characters</label>
      <div class="chip-row" id="m-characters">${chipRowHTML(f.characters, 'characters')}</div>
      <div class="row" style="margin-top:0.35rem">
        <input id="m-char-val" placeholder="Add character" autocapitalize="off" autocomplete="off"/>
        <button type="button" class="secondary" data-add-list="characters">Add</button>
      </div>
    </div>
  `
}

function refreshAccountModalLists() {
  if (!accountForm) return
  const root = $('#modal-root')
  const aliases = root.querySelector('#m-aliases')
  const tags = root.querySelector('#m-tags')
  const chars = root.querySelector('#m-characters')
  if (aliases) aliases.innerHTML = chipRowHTML(accountForm.aliases, 'aliases')
  if (tags) tags.innerHTML = chipRowHTML(accountForm.tags, 'tags')
  if (chars) chars.innerHTML = chipRowHTML(accountForm.characters, 'characters')
  bindAccountModalListActions(root)
}

function bindAccountModalListActions(root) {
  root.querySelectorAll('[data-rm-list]').forEach((btn) => {
    btn.onclick = () => run(async () => {
      const list = btn.dataset.rmList
      const val = btn.dataset.rmVal
      if (!accountForm || !list || !val) return
      if (accountForm.id) {
        const path = list === 'aliases' ? 'aliases' : list === 'tags' ? 'tags' : 'characters'
        const field = list === 'aliases' ? 'alias' : list === 'tags' ? 'tag' : 'name'
        await api(`/admin/api/accounts/${accountForm.id}/${path}`, {
          method: 'DELETE',
          body: JSON.stringify({ [field]: val }),
        })
        accountForm[list] = accountForm[list].filter((x) => x.toLowerCase() !== val.toLowerCase())
      } else {
        accountForm[list] = accountForm[list].filter((x) => x.toLowerCase() !== val.toLowerCase())
      }
      refreshAccountModalLists()
    })
  })
}

function openAccountModal(account) {
  if (account) {
    accountForm = {
      id: account.id,
      username: account.username || '',
      restricted: !!account.restricted,
      disabled: !!account.disabled,
      required_role_ids: (account.required_role_ids && account.required_role_ids.length)
        ? [...account.required_role_ids]
        : (account.required_role_id ? [account.required_role_id] : []),
      required_user_ids: (account.required_user_ids && account.required_user_ids.length)
        ? [...account.required_user_ids]
        : (account.required_user_id ? [account.required_user_id] : []),
      group_ids: [...(account.group_ids || [])],
      aliases: (account.aliases || []).filter((al) => !account.username || al.toLowerCase() !== account.username.toLowerCase()),
      tags: [...(account.tags || [])],
      characters: [...(account.characters || [])],
    }
  } else {
    accountForm = {
      id: null,
      username: '',
      restricted: false,
      disabled: false,
      required_role_ids: [],
      required_user_ids: [],
      group_ids: [],
      aliases: [],
      tags: [],
      characters: [],
    }
  }
  const isEdit = !!accountForm.id
  const root = $('#modal-root')
  root.innerHTML = `
    <div class="modal-backdrop" data-close="1">
      <div class="modal wide" role="dialog">
        <h2>${isEdit ? 'Edit account' : 'Add account'}</h2>
        <div class="form-grid" id="account-modal-body">${renderAccountModalBody()}</div>
        <div class="modal-actions">
          ${isEdit ? `<button type="button" class="danger" data-del-acct="1" style="margin-right:auto">Remove</button>` : ''}
          <button type="button" class="secondary" data-cancel="1">Cancel</button>
          <button type="button" data-save="1">${isEdit ? 'Save' : 'Create'}</button>
        </div>
      </div>
    </div>`
  root.querySelector('[data-close]').addEventListener('click', (e) => {
    if (e.target.dataset.close) { accountForm = null; closeModal() }
  })
  root.querySelector('[data-cancel]').addEventListener('click', () => { accountForm = null; closeModal() })
  root.querySelector('[data-del-acct]')?.addEventListener('click', () => run(async () => {
    if (!accountForm?.id) return
    if (!confirm(`Remove account ${accountForm.username || accountForm.id}?`)) return
    await api(`/admin/api/accounts/${accountForm.id}`, { method: 'DELETE' })
    accountForm = null
    closeModal()
  }))
  bindAccountModalListActions(root)
  root.querySelectorAll('[data-add-list]').forEach((btn) => {
    btn.onclick = () => run(async () => {
      const list = btn.dataset.addList
      const inputId = list === 'aliases' ? 'm-alias-val' : list === 'tags' ? 'm-tag-val' : 'm-char-val'
      const input = root.querySelector(`#${inputId}`)
      const val = (input?.value || '').trim()
      if (!val) throw new Error(`${list.slice(0, -1)} required`)
      if (accountForm[list].some((x) => x.toLowerCase() === val.toLowerCase())) {
        input.value = ''
        return
      }
      if (accountForm.id) {
        const path = list === 'aliases' ? 'aliases' : list === 'tags' ? 'tags' : 'characters'
        const field = list === 'aliases' ? 'alias' : list === 'tags' ? 'tag' : 'name'
        await api(`/admin/api/accounts/${accountForm.id}/${path}`, {
          method: 'POST',
          body: JSON.stringify({ [field]: val }),
        })
      }
      accountForm[list].push(val)
      if (input) input.value = ''
      refreshAccountModalLists()
    })
  })
  root.querySelector('[data-save]').addEventListener('click', () => run(async () => {
    const access = accountForm.restricted ? {} : readAccessFields(root)
    if (accountForm.id) {
      const password = root.querySelector('#m-pass').value
      const disabled = !!root.querySelector('#m-dis')?.checked
      const body = { disabled, ...access }
      if (password) body.password = password
      await api(`/admin/api/accounts/${accountForm.id}`, { method: 'PATCH', body: JSON.stringify(body) })
    } else {
      const username = root.querySelector('#m-user').value.trim()
      const password = root.querySelector('#m-pass').value
      if (!username || !password) throw new Error('username and password required')
      const res = await api('/admin/api/accounts', {
        method: 'POST',
        body: JSON.stringify({
          username,
          password,
          required_role_id: (access.required_role_ids && access.required_role_ids[0]) || '',
        }),
      })
      const id = res.account_id
      if ((access.required_role_ids && access.required_role_ids.length)
        || (access.required_user_ids && access.required_user_ids.length)
        || (access.group_ids && access.group_ids.length)) {
        await api(`/admin/api/accounts/${id}`, { method: 'PATCH', body: JSON.stringify(access) })
      }
      for (const al of accountForm.aliases) {
        await api(`/admin/api/accounts/${id}/aliases`, { method: 'POST', body: JSON.stringify({ alias: al }) })
      }
      for (const t of accountForm.tags) {
        await api(`/admin/api/accounts/${id}/tags`, { method: 'POST', body: JSON.stringify({ tag: t }) })
      }
      for (const ch of accountForm.characters) {
        await api(`/admin/api/accounts/${id}/characters`, { method: 'POST', body: JSON.stringify({ name: ch }) })
      }
    }
    accountForm = null
    closeModal()
  }))
}

function bindAccounts(root) {
  root.querySelector('#import-csv')?.addEventListener('click', () => {
    root.querySelector('#csv-file')?.click()
  })
  root.querySelector('#export-csv')?.addEventListener('click', () => {
    run(async () => {
      const res = await fetch('/admin/api/accounts/export', { credentials: 'same-origin' })
      if (res.status === 401) {
        location.href = '/admin/login'
        return
      }
      if (!res.ok) {
        let msg = 'Export failed'
        try {
          const j = await res.json()
          if (j.error) msg = j.error
        } catch (_) {}
        throw new Error(msg)
      }
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'alfred-sso-accounts.csv'
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
      showError('')
    })
  })
  root.querySelector('#csv-file')?.addEventListener('change', (ev) => {
    const file = ev.target.files && ev.target.files[0]
    ev.target.value = ''
    if (!file) return
    run(async () => {
      const fd = new FormData()
      fd.append('file', file)
      const res = await api('/admin/api/accounts/import', { method: 'POST', body: fd })
      const parts = [`Imported: ${res.added || 0} added, ${res.updated || 0} updated`]
      if (res.errors && res.errors.length) {
        parts.push(`${res.errors.length} row issue(s): ${res.errors.slice(0, 5).join('; ')}${res.errors.length > 5 ? '…' : ''}`)
        showError(parts.join(' · '))
      } else {
        showError('')
        alert(parts[0])
      }
    })
  })

  root.querySelector('#add-account')?.addEventListener('click', () => openAccountModal(null))
  root.querySelectorAll('[data-edit]').forEach((btn) => {
    btn.onclick = () => {
      const a = state.accounts.find((x) => String(x.id) === btn.dataset.edit)
      if (a) openAccountModal(a)
    }
  })
}

function openUserEditModal(user) {
  const memberOf = new Set(groupsForUser(user.id).map((g) => g.id))
  const roles = (user.role_ids || []).map(roleName).join(', ') || '—'
  const groupItems = (state.groups || []).map((g) => `
    <label class="role-item">
      <input type="checkbox" name="m-user-group" value="${g.id}" ${memberOf.has(g.id) ? 'checked' : ''}/>
      <span><strong>${esc(g.name)}</strong>${g.description ? `<div class="muted">${esc(g.description)}</div>` : ''}</span>
    </label>`).join('') || '<p class="empty">No groups yet. Create one on the Groups tab.</p>'
  modal(`Edit user — ${user.display_name || user.discord_id}`, `
    <p class="hint">Discord roles are synced from Discord and cannot be edited here.</p>
    <div><label>Discord roles</label><div class="muted">${esc(roles)}</div></div>
    <div class="form-span">
      <label>Access groups</label>
      <p class="hint">Membership lets this user use EQ accounts linked to the same group.</p>
      <div class="role-list">${groupItems}</div>
    </div>
    <label class="role-item">
      <input id="m-revoked" type="checkbox" ${user.access_revoked ? 'checked' : ''}/>
      <span>SSO access revoked</span>
    </label>
  `, (el) => run(async () => {
    const selected = new Set([...el.querySelectorAll('input[name=m-user-group]:checked')].map((i) => Number(i.value)))
    for (const g of state.groups || []) {
      const was = memberOf.has(g.id)
      const now = selected.has(g.id)
      if (!was && now) {
        await api(`/admin/api/groups/${g.id}/users`, {
          method: 'POST', body: JSON.stringify({ user_id: user.id }),
        })
      } else if (was && !now) {
        await api(`/admin/api/groups/${g.id}/users`, {
          method: 'DELETE', body: JSON.stringify({ user_id: user.id }),
        })
      }
    }
    const revoked = !!el.querySelector('#m-revoked')?.checked
    if (revoked !== !!user.access_revoked) {
      await api(`/admin/api/users/${user.id}/access`, {
        method: 'PATCH', body: JSON.stringify({ revoked }),
      })
    }
    closeModal()
  }))
}

function bindUsers(root) {
  root.querySelectorAll('[data-edit-user]').forEach((btn) => {
    btn.onclick = () => {
      const u = state.users.find((x) => String(x.id) === btn.dataset.editUser)
      if (u) openUserEditModal(u)
    }
  })
}

function userByID(id) {
  return (state.users || []).find((x) => x.id === id) || { id, display_name: `#${id}`, discord_id: '' }
}

function groupByID(id) {
  return (state.groups || []).find((x) => x.id === id)
}

function groupsForUser(userId) {
  const uid = Number(userId)
  return (state.groups || []).filter((g) =>
    (g.user_ids || []).some((id) => Number(id) === uid)
    || (g.users || []).some((u) => Number(u.id) === uid))
}

function groupNamesForAccount(a) {
  return (a.group_ids || []).map((id) => groupByID(id)?.name || `#${id}`).filter(Boolean)
}

function accountAccessLabel(a) {
  if (a.disabled) return 'disabled'
  if (a.restricted) {
    if (isShareOwner(a)) return 'private share (owner)'
    return 'private share (shared with you)'
  }
  const parts = []
  const roleIDs = (a.required_role_ids && a.required_role_ids.length)
    ? a.required_role_ids
    : (a.required_role_id ? [a.required_role_id] : [])
  for (const rid of roleIDs) parts.push(roleName(rid))
  const userIDs = (a.required_user_ids && a.required_user_ids.length)
    ? a.required_user_ids
    : (a.required_user_id ? [a.required_user_id] : [])
  for (const uid of userIDs) {
    const u = userByID(uid)
    parts.push(u.display_name || u.discord_id || `#${uid}`)
  }
  for (const gid of a.group_ids || []) {
    const g = groupByID(gid)
    parts.push(g?.name || `group #${gid}`)
  }
  return parts.length ? parts.map(esc).join(', ') : 'all'
}

function accessFieldsHTML(a) {
  const selectedRoles = new Set(
    (a?.required_role_ids && a.required_role_ids.length)
      ? a.required_role_ids
      : (a?.required_role_id ? [a.required_role_id] : []),
  )
  const selectedUsers = new Set(
    (a?.required_user_ids && a.required_user_ids.length)
      ? a.required_user_ids.map(Number)
      : (a?.required_user_id ? [Number(a.required_user_id)] : []),
  )
  const roleItems = (state.roles || []).map((r) => `
    <label class="role-item">
      <input type="checkbox" name="m-role" value="${esc(r.id)}" ${selectedRoles.has(r.id) ? 'checked' : ''}/>
      <span><strong>${esc(r.name || r.id)}</strong><div class="muted mono">${esc(r.id)}</div></span>
    </label>`).join('') || '<p class="empty">No Discord roles cached yet.</p>'
  const userItems = (state.users || []).map((u) => `
    <label class="role-item">
      <input type="checkbox" name="m-user-access" value="${u.id}" ${selectedUsers.has(u.id) ? 'checked' : ''}/>
      <span><strong>${esc(u.display_name || 'Unknown')}</strong><div class="muted mono">${esc(u.discord_id || '')}</div></span>
    </label>`).join('') || '<p class="empty">No Discord SSO users yet.</p>'
  const selected = new Set((a?.group_ids || []).map(Number))
  const groupItems = (state.groups || []).map((g) => `
    <label class="role-item">
      <input type="checkbox" name="m-group" value="${g.id}" ${selected.has(g.id) ? 'checked' : ''}/>
      <span><strong>${esc(g.name)}</strong>${g.description ? `<div class="muted">${esc(g.description)}</div>` : ''}</span>
    </label>`).join('') || '<p class="empty">No groups yet. Create one on the Groups tab.</p>'
  return `
    <div class="form-span">
      <label>Discord roles</label>
      <div class="role-list">${roleItems}</div>
    </div>
    <div class="form-span">
      <label>Discord users</label>
      <div class="role-list">${userItems}</div>
    </div>
    <div class="form-span">
      <label>Groups</label>
      <div class="role-list" id="m-groups">${groupItems}</div>
    </div>
    <p class="hint">Leave roles, users, and groups empty for <strong>all</strong> SSO users. If any are set, the login user must match at least one grant (any selected role <em>or</em> user <em>or</em> group).</p>
  `
}

function readAccessFields(el) {
  const required_role_ids = [...el.querySelectorAll('input[name=m-role]:checked')].map((i) => i.value)
  const required_user_ids = [...el.querySelectorAll('input[name=m-user-access]:checked')].map((i) => Number(i.value))
  const group_ids = [...el.querySelectorAll('input[name=m-group]:checked')].map((i) => Number(i.value))
  return { required_role_ids, required_user_ids, group_ids }
}

function renderGroups() {
  const list = sortRows(state.groups || [], 'groups', {
    name: (g) => g.name || '',
    web: (g) => g.web_role || '',
    users: (g) => (g.users || g.user_ids || []).length,
    roles: (g) => (g.role_ids || []).length,
    accounts: (g) => (g.account_ids || []).length,
  })
  const rows = list.map((g) => {
    const memberUsers = (g.users && g.users.length)
      ? g.users
      : (g.user_ids || []).map((id) => userByID(id))
    const usersLabel = memberUsers.length
      ? memberUsers.map((u) => u.display_name || u.discord_id || `#${u.id}`).join(', ')
      : '—'
    const rolesLabel = (g.role_ids || []).length
      ? (g.role_ids || []).map((id) => roleName(id)).join(', ')
      : '—'
    const accountsLabel = (g.account_ids || []).length
      ? (g.account_ids || []).map((id) => {
          const a = (state.accounts || []).find((x) => x.id === id)
          return a?.username || `#${id}`
        }).join(', ')
      : '—'
    const web = g.web_role || ''
    const cmds = discordCommandsLabel(g.discord_commands)
    return `<tr>
      <td class="col-name">
        <strong>${esc(g.name)}</strong>
        ${g.description ? `<div class="muted">${esc(g.description)}</div>` : ''}
      </td>
      <td>${esc(webRoleLabel(web))}${web ? '' : '<div class="muted">No web login</div>'}</td>
      <td>${esc(cmds)}${cmds === '—' ? '<div class="muted">No slash commands</div>' : ''}</td>
      <td>${esc(usersLabel)}</td>
      <td>${esc(rolesLabel)}</td>
      <td class="mono">${esc(accountsLabel)}</td>
      <td class="col-actions">
        <button type="button" class="secondary" data-edit-group="${g.id}" ${isWebAdmin() ? '' : 'disabled title="Read-only access"'}>Edit</button>
      </td>
    </tr>`
  }).join('')

  return `
    <section class="panel">
      <div class="row head">
        <h2>Access groups</h2>
        <button type="button" id="add-group" ${isWebAdmin() ? '' : 'disabled title="Read-only access"'}>Create</button>
      </div>
      <p class="hint">
        Groups bundle Discord <strong>users</strong> and/or <strong>roles</strong>, and link EQ <strong>accounts</strong>.
        Optionally grant <strong>web UI</strong> access (admin or read-only) and/or <strong>Discord slash commands</strong> to group members.
        When any group enables a slash command, only members of groups with that command may use it.
        Discord admin role and bootstrap admins always have full web access and bypass slash command restrictions.
      </p>
      <div class="table-wrap">
        <table class="data-table groups-table">
          <colgroup>
            <col class="w-name"/><col/><col/><col class="w-users"/><col class="w-roles"/><col class="w-accts"/><col class="w-actions"/>
          </colgroup>
          <thead><tr>
            ${sortTh('groups', 'name', 'Name')}
            ${sortTh('groups', 'web', 'Web UI')}
            <th>Discord commands</th>
            ${sortTh('groups', 'users', 'Discord users')}
            ${sortTh('groups', 'roles', 'Discord roles')}
            ${sortTh('groups', 'accounts', 'EQ accounts')}
            <th class="col-actions"></th>
          </tr></thead>
          <tbody>${rows || '<tr><td colspan="7" class="empty">No groups yet.</td></tr>'}</tbody>
        </table>
      </div>
    </section>`
}

function openGroupModal(group) {
  const isEdit = !!(group && group.id)
  const g = group || {}
  const selectedUsers = new Set((g.user_ids || []).map(Number))
  const selectedRoles = new Set(g.role_ids || [])
  const selectedAccts = new Set((g.account_ids || []).map(Number))

  const userItems = (state.users || []).map((u) => `
    <label class="role-item">
      <input type="checkbox" name="m-g-user" value="${u.id}" ${selectedUsers.has(u.id) ? 'checked' : ''}/>
      <span><strong>${esc(u.display_name || 'Unknown')}</strong><div class="muted mono">${esc(u.discord_id || '')}</div></span>
    </label>`).join('') || '<p class="empty">No Discord SSO users yet.</p>'

  const roleItems = (state.roles || []).map((r) => `
    <label class="role-item">
      <input type="checkbox" name="m-g-role" value="${esc(r.id)}" ${selectedRoles.has(r.id) ? 'checked' : ''}/>
      <span><strong>${esc(r.name || r.id)}</strong><div class="muted mono">${esc(r.id)}</div></span>
    </label>`).join('') || '<p class="empty">No Discord roles cached yet.</p>'

  const acctItems = (state.accounts || []).filter((a) => !a.restricted).map((a) => `
    <label class="role-item">
      <input type="checkbox" name="m-g-acct" value="${a.id}" ${selectedAccts.has(a.id) ? 'checked' : ''}/>
      <span><strong class="mono">${esc(a.username || '#' + a.id)}</strong></span>
    </label>`).join('') || '<p class="empty">No EQ accounts yet.</p>'

  const root = $('#modal-root')
  root.innerHTML = `
    <div class="modal-backdrop" data-close="1">
      <div class="modal wide" role="dialog">
        <h2>${isEdit ? 'Edit group' : 'Create access group'}</h2>
        <div class="form-grid">
          <div><label>Name</label><input id="m-name" value="${esc(g.name || '')}" autocomplete="off" placeholder="e.g. Raid officers"/></div>
          <div><label>Description</label><input id="m-desc" value="${esc(g.description || '')}" autocomplete="off"/></div>
          <div class="form-span">
            <label>Web UI access</label>
            <p class="hint">Optional. Members of this group may sign in to the web admin with the selected permission level.</p>
            <select id="m-web-role">
              <option value="" ${!(g.web_role) ? 'selected' : ''}>Off — no web login from this group</option>
              <option value="readonly" ${g.web_role === 'readonly' ? 'selected' : ''}>Read-only — view only</option>
              <option value="admin" ${g.web_role === 'admin' ? 'selected' : ''}>Admin — full manage access</option>
            </select>
          </div>
          <div class="form-span">
            <label>Discord slash commands</label>
            <p class="hint">Members may use the selected bot commands. When any group enables a command, users outside those groups are denied.</p>
            <div class="role-list">${discordCommandsFieldHTML(g.discord_commands || [])}</div>
          </div>
          <div class="form-span">
            <label>Discord users</label>
            <p class="hint">Select none, one, or more. Members can use linked EQ accounts.</p>
            <div class="role-list">${userItems}</div>
          </div>
          <div class="form-span">
            <label>Discord roles</label>
            <p class="hint">Anyone with a selected Discord role is treated as a group member.</p>
            <div class="role-list">${roleItems}</div>
          </div>
          <div class="form-span">
            <label>EQ accounts</label>
            <p class="hint">Accounts linked here become available to group members at SSO login.</p>
            <div class="role-list">${acctItems}</div>
          </div>
        </div>
        <div class="modal-actions">
          ${isEdit ? '<button type="button" class="danger" data-del-group="1">Delete</button>' : ''}
          <button type="button" class="secondary" data-cancel="1">Cancel</button>
          <button type="button" data-save="1">${isEdit ? 'Save' : 'Create'}</button>
        </div>
      </div>
    </div>`
  root.querySelector('[data-close]').addEventListener('click', (e) => {
    if (e.target.dataset.close) closeModal()
  })
  root.querySelector('[data-cancel]').addEventListener('click', closeModal)
  if (isEdit) {
    root.querySelector('[data-del-group]').addEventListener('click', () => run(async () => {
      if (!confirm('Delete this group? Account links and memberships are removed.')) return
      await api(`/admin/api/groups/${group.id}`, { method: 'DELETE' })
      closeModal()
    }))
  }
  root.querySelector('[data-save]').addEventListener('click', () => run(async () => {
    const name = root.querySelector('#m-name').value.trim()
    if (!name) throw new Error('name required')
    const payload = {
      name,
      description: root.querySelector('#m-desc').value.trim(),
      web_role: root.querySelector('#m-web-role').value,
      discord_commands: readDiscordCommandsFromModal(root),
      user_ids: [...root.querySelectorAll('input[name=m-g-user]:checked')].map((i) => Number(i.value)),
      role_ids: [...root.querySelectorAll('input[name=m-g-role]:checked')].map((i) => i.value),
      account_ids: [...root.querySelectorAll('input[name=m-g-acct]:checked')].map((i) => Number(i.value)),
    }
    if (isEdit) {
      await api(`/admin/api/groups/${group.id}`, { method: 'PATCH', body: JSON.stringify(payload) })
    } else {
      await api('/admin/api/groups', { method: 'POST', body: JSON.stringify(payload) })
    }
    closeModal()
  }))
}

function openEditGroupModal(group) {
  openGroupModal(group)
}

function bindGroups(root) {
  root.querySelector('#add-group')?.addEventListener('click', () => {
    if (!isWebAdmin()) return
    openGroupModal(null)
  })
  root.querySelectorAll('[data-edit-group]').forEach((btn) => {
    btn.onclick = () => {
      const g = state.groups.find((x) => String(x.id) === btn.dataset.editGroup)
      if (g) openEditGroupModal(g)
    }
  })
}

function shareGrantsHTML(a) {
  const parts = []
  for (const uid of a.shared_user_ids || []) {
    const u = userByID(uid)
    parts.push(`<div class="stack-item">${discordUserHTML(u)}</div>`)
  }
  const roleIDs = (a.required_role_ids && a.required_role_ids.length)
    ? a.required_role_ids
    : (a.required_role_id ? [a.required_role_id] : [])
  for (const rid of roleIDs) {
    parts.push(`<div class="stack-item"><span class="badge">role</span> ${esc(roleName(rid))}</div>`)
  }
  for (const gid of a.group_ids || []) {
    const g = groupByID(gid)
    parts.push(`<div class="stack-item"><span class="badge">group</span> ${esc(g?.name || `group #${gid}`)}</div>`)
  }
  return parts.length ? parts.join('') : '<span class="muted">Owner only</span>'
}

function renderShares() {
  const shared = sortRows(state.shares || [], 'shares', {
    username: (a) => a.username || '',
    owner: (a) => {
      const o = a.owner_user_id ? userByID(a.owner_user_id) : null
      return o?.display_name || o?.discord_id || ''
    },
    sharees: (a) => (a.shared_user_ids || []).length + (a.group_ids || []).length + ((a.required_role_ids && a.required_role_ids.length) || a.required_role_id ? 1 : 0),
  })
	const rows = shared.map((a) => {
    const owner = a.owner_user_id ? userByID(a.owner_user_id) : null
    return `<tr>
      <td class="mono">${esc(a.username || '#' + a.id)}</td>
      <td>${owner ? discordUserHTML(owner) : '<span class="muted">—</span>'}</td>
      <td class="col-stack"><div class="stack-list">${shareGrantsHTML(a)}</div></td>
      <td class="col-actions">
        ${canManageShare(a)
    ? `<button type="button" class="danger" data-del-share="${a.id}">Remove</button>`
    : '<span class="muted">—</span>'}
      </td>
    </tr>`
  }).join('')

  return `
    <section class="panel">
      <div class="row head">
        <h2>Shared accounts</h2>
      </div>
      <p class="hint">
        Private SSO copies from desktop <strong>Local → Share</strong>. Owners choose Discord users, roles, and/or access groups.
        Remove deletes the private SSO copy (local CSV on the owner’s machine is unchanged).
      </p>
      <div class="table-wrap">
        <table class="data-table shared-table">
          <colgroup>
            <col class="w-acct"/><col class="w-owner"/><col class="w-sharees"/><col class="w-actions"/>
          </colgroup>
          <thead><tr>
            ${sortTh('shares', 'username', 'EQ account')}
            ${sortTh('shares', 'owner', 'Owner')}
            ${sortTh('shares', 'sharees', 'Shared with')}
            <th class="col-actions"></th>
          </tr></thead>
          <tbody>${rows || '<tr><td colspan="4" class="empty">No private shared accounts yet. Users create them from the desktop GUI (Local → Share).</td></tr>'}</tbody>
        </table>
      </div>
    </section>`
}

function bindShares(root) {
  root.querySelectorAll('[data-del-share]').forEach((btn) => {
    btn.onclick = () => run(async () => {
      const a = (state.shares || []).find((x) => String(x.id) === btn.dataset.delShare)
      const label = a?.username || `#${btn.dataset.delShare}`
      if (!confirm(`Remove private share “${label}”? This deletes the SSO copy for everyone.`)) return
      await api(`/admin/api/accounts/${btn.dataset.delShare}`, { method: 'DELETE' })
    })
  })
}


const AUDIT_ACTION_LABELS = {
  web_add_account: 'Account created',
  web_update_account: 'Account updated',
  web_remove_account: 'Account removed',
  web_add_alias: 'Alias added',
  web_remove_alias: 'Alias removed',
  web_add_tag: 'Tag added',
  web_remove_tag: 'Tag removed',
  web_add_character: 'Character added',
  web_remove_character: 'Character removed',
  web_set_account_shares: 'Shares updated',
  web_import_accounts: 'CSV import',
  web_export_accounts: 'CSV export',
  web_export_config: 'Config export',
  web_import_config: 'Config import',
  web_group_link_account: 'Linked to group',
  web_group_unlink_account: 'Unlinked from group',
  admin_add_account: 'Account created (GUI)',
  admin_update_account: 'Account updated (GUI)',
  admin_remove_account: 'Account removed (GUI)',
  admin_add_alias: 'Alias added (GUI)',
  admin_remove_alias: 'Alias removed (GUI)',
  admin_add_tag: 'Tag added (GUI)',
  admin_remove_tag: 'Tag removed (GUI)',
  admin_add_character: 'Character added (GUI)',
  admin_remove_character: 'Character removed (GUI)',
  share_account: 'Local account shared',
  unshare_account: 'Local share removed',
  login_auth: 'SSO login',
}

function auditActionLabel(action) {
  return AUDIT_ACTION_LABELS[action] || action
}

function formatAuditTime(iso) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return esc(iso)
  return esc(d.toLocaleString())
}

function buildAuditAccountOptions() {
  return (state.accounts || []).slice()
    .sort((a, b) => String(a.username || '').localeCompare(String(b.username || ''), undefined, { sensitivity: 'base' }))
    .map((a) => {
      const aliases = (a.aliases || []).filter((x) => !a.username || x.toLowerCase() !== a.username.toLowerCase())
      const tags = a.tags || []
      const subParts = []
      if (aliases.length) subParts.push(aliases.join(', '))
      if (tags.length) subParts.push(tags.join(', '))
      if (a.disabled) subParts.push('disabled')
      return {
        value: a.id,
        label: a.username || `#${a.id}`,
        sub: subParts.join(' · ') || `#${a.id}`,
        search: [a.username, ...aliases, ...tags, String(a.id)].filter(Boolean).join(' ').toLowerCase(),
      }
    })
}

function buildAuditUserOptions() {
  return (state.users || []).slice()
    .sort((a, b) => String(a.display_name || a.discord_id || '')
      .localeCompare(String(b.display_name || b.discord_id || ''), undefined, { sensitivity: 'base' }))
    .map((u) => {
      const subParts = []
      if (u.display_name && u.discord_id) subParts.push(u.discord_id)
      else subParts.push(`#${u.id}`)
      if (u.access_revoked) subParts.push('revoked')
      return {
        value: u.id,
        label: u.display_name || u.discord_id || `#${u.id}`,
        sub: subParts.join(' · '),
        search: [u.display_name, u.discord_id, String(u.id)].filter(Boolean).join(' ').toLowerCase(),
      }
    })
}

function renderSearchSelect({ id, allLabel, value, options, placeholder }) {
  const allOption = { value: 0, label: allLabel, sub: '', search: allLabel.toLowerCase() }
  const selected = value === 0
    ? allOption
    : options.find((o) => o.value === value) || allOption
  const optionHtml = [allOption, ...options].map((o) => `
    <button type="button" class="search-select-option${o.value === value ? ' selected' : ''}"
      data-value="${o.value}" data-search="${esc(o.search || '')}" role="option"
      aria-selected="${o.value === value ? 'true' : 'false'}">
      <span class="search-select-option-label">${esc(o.label)}</span>
      ${o.sub ? `<span class="search-select-option-sub muted">${esc(o.sub)}</span>` : ''}
    </button>`).join('')
  return `
    <div class="search-select" data-search-select="${esc(id)}">
      <button type="button" class="search-select-trigger" aria-haspopup="listbox" aria-expanded="false">
        <span class="search-select-value">
          <span class="search-select-label">${esc(selected.label)}</span>
          ${selected.sub ? `<span class="search-select-sub muted">${esc(selected.sub)}</span>` : ''}
        </span>
        <span class="search-select-caret" aria-hidden="true">▾</span>
      </button>
      <div class="search-select-menu hidden" role="listbox">
        <div class="search-select-search-wrap">
          <input type="search" class="search-select-search" placeholder="${esc(placeholder)}"
            autocomplete="off" aria-label="${esc(placeholder)}">
        </div>
        <div class="search-select-list">${optionHtml}</div>
      </div>
    </div>`
}

function bindSearchSelects(root, handlers) {
  root.querySelectorAll('[data-search-select]').forEach((wrap) => {
    const id = wrap.dataset.searchSelect
    const trigger = wrap.querySelector('.search-select-trigger')
    const menu = wrap.querySelector('.search-select-menu')
    const search = wrap.querySelector('.search-select-search')
    const list = wrap.querySelector('.search-select-list')
    const onChange = handlers[id]
    if (!trigger || !menu || !search || !list || !onChange) return

    let outsideHandler = null
    const close = () => {
      menu.classList.add('hidden')
      trigger.setAttribute('aria-expanded', 'false')
      if (outsideHandler) {
        document.removeEventListener('click', outsideHandler)
        outsideHandler = null
      }
    }
    const filterOptions = (query) => {
      const needle = query.trim().toLowerCase()
      list.querySelectorAll('.search-select-option').forEach((opt) => {
        const hay = opt.dataset.search || opt.textContent.toLowerCase()
        opt.hidden = Boolean(needle && !hay.includes(needle))
      })
    }
    const open = () => {
      root.querySelectorAll('[data-search-select]').forEach((other) => {
        if (other === wrap) return
        other.querySelector('.search-select-menu')?.classList.add('hidden')
        other.querySelector('.search-select-trigger')?.setAttribute('aria-expanded', 'false')
      })
      menu.classList.remove('hidden')
      trigger.setAttribute('aria-expanded', 'true')
      search.value = ''
      filterOptions('')
      search.focus()
      outsideHandler = (e) => {
        if (!wrap.contains(e.target)) close()
      }
      setTimeout(() => document.addEventListener('click', outsideHandler), 0)
    }
    const selectOption = (opt) => {
      const value = Number(opt.dataset.value) || 0
      close()
      onChange(value)
    }

    trigger.addEventListener('click', (e) => {
      e.stopPropagation()
      if (menu.classList.contains('hidden')) open()
      else close()
    })
    search.addEventListener('input', () => filterOptions(search.value))
    search.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        close()
        trigger.focus()
      }
    })
    list.querySelectorAll('.search-select-option').forEach((opt) => {
      opt.addEventListener('click', (e) => {
        e.stopPropagation()
        selectOption(opt)
      })
    })
  })
}

async function loadAudit(reset) {
  if (auditLoading) return
  auditLoading = true
  try {
    if (reset) {
      auditOffset = 0
      auditEntries = []
    }
    const q = new URLSearchParams({ limit: String(AUDIT_PAGE), offset: String(auditOffset) })
    if (auditFilterAccount > 0) q.set('account_id', String(auditFilterAccount))
    if (auditFilterUser > 0) q.set('user_id', String(auditFilterUser))
    const res = await api('/admin/api/audit?' + q.toString())
    const batch = res.entries || []
    auditEntries = reset ? batch : auditEntries.concat(batch)
    auditOffset = auditEntries.length
    if (tab === 'audit') render()
  } catch (e) {
    showError(String(e.message || e))
  } finally {
    auditLoading = false
  }
}

function renderAudit() {
  const list = sortRows(auditEntries, 'audit', {
    created: (e) => e.created_at || '',
    actor: (e) => e.actor_name || e.actor_discord_id || '',
    account: (e) => e.account_username || (e.account_id ? String(e.account_id) : ''),
    action: (e) => auditActionLabel(e.action),
    detail: (e) => e.detail || '',
  })
  // Default sort is newest first when key is created and dir -1
  const rows = list.map((e) => {
    const actor = e.actor_name || e.actor_discord_id
      ? `${esc(e.actor_name || 'Unknown')}${e.actor_discord_id ? `<div class="muted mono">${esc(e.actor_discord_id)}</div>` : ''}`
      : '<span class="muted">—</span>'
    const acct = e.account_username
      ? `<span class="mono">${esc(e.account_username)}</span>`
      : (e.account_id ? `<span class="mono muted">#${e.account_id}</span>` : '<span class="muted">—</span>')
    return `<tr>
      <td class="mono muted">${formatAuditTime(e.created_at)}</td>
      <td>${actor}</td>
      <td>${acct}</td>
      <td>${esc(auditActionLabel(e.action))}<div class="muted mono">${esc(e.action)}</div></td>
      <td class="mono">${esc(e.detail || '—')}</td>
    </tr>`
  }).join('')

  const acctOpts = buildAuditAccountOptions()
  const userOpts = buildAuditUserOptions()

  return `
    <section class="panel">
      <div class="row head">
        <h2>Account audit log</h2>
        <div class="actions audit-filters">
          ${renderSearchSelect({
            id: 'audit-user-filter',
            allLabel: 'All Discord users',
            value: auditFilterUser,
            options: userOpts,
            placeholder: 'Search users…',
          })}
          ${renderSearchSelect({
            id: 'audit-account-filter',
            allLabel: 'All EQ accounts',
            value: auditFilterAccount,
            options: acctOpts,
            placeholder: 'Search accounts…',
          })}
          <button type="button" class="secondary" id="audit-refresh">Refresh</button>
        </div>
      </div>
      <p class="hint">Creates, updates, aliases, tags, characters, shares, group links, imports, and SSO logins.</p>
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr>
            ${sortTh('audit', 'created', 'When')}
            ${sortTh('audit', 'actor', 'Actor')}
            ${sortTh('audit', 'account', 'Account')}
            ${sortTh('audit', 'action', 'Action')}
            ${sortTh('audit', 'detail', 'Detail')}
          </tr></thead>
          <tbody>${rows || `<tr><td colspan="5" class="empty">${auditLoading ? 'Loading…' : 'No account audit events yet.'}</td></tr>`}</tbody>
        </table>
      </div>
      <div class="row" style="margin-top:0.75rem">
        <button type="button" class="secondary" id="audit-more" ${auditLoading ? 'disabled' : ''}>Load more</button>
        <span class="muted">Showing ${auditEntries.length} event${auditEntries.length === 1 ? '' : 's'}</span>
      </div>
    </section>`
}

function bindAudit(root) {
  root.querySelector('#audit-refresh')?.addEventListener('click', () => loadAudit(true))
  root.querySelector('#audit-more')?.addEventListener('click', () => loadAudit(false))
  bindSearchSelects(root, {
    'audit-account-filter': (value) => {
      auditFilterAccount = value
      loadAudit(true)
    },
    'audit-user-filter': (value) => {
      auditFilterUser = value
      loadAudit(true)
    },
  })
}

function renderSettings() {
  const admin = isWebAdmin()
  return `
    <section class="panel">
      <h2>Settings</h2>
      <p class="hint">
        Export or import a full configuration backup for migrating to another host.
        The file is JSON and includes users (by Discord ID), access groups, and EQ accounts
        (with passwords, aliases, tags, characters, access grants, and private shares).
      </p>
      ${admin ? '' : '<p class="banner err">Read-only access — configuration backup requires admin.</p>'}

      <div class="settings-block">
        <h3>Export configuration</h3>
        <p class="hint">Download a portable backup. Use this on the source host before migrating.</p>
        <button type="button" class="secondary" id="export-config" ${admin ? '' : 'disabled'}>Download backup JSON</button>
      </div>

      <div class="settings-block">
        <h3>Import configuration</h3>
        <p class="hint">
          Merge into this host: users and groups upsert by Discord ID / group name;
          accounts upsert by EQ username (passwords overwrite when present).
          API tokens are not migrated — users must create new tokens on the new host.
        </p>
        <div class="row">
          <button type="button" id="import-config" ${admin ? '' : 'disabled'}>Upload backup JSON…</button>
          <input type="file" id="config-file" accept=".json,application/json" hidden/>
        </div>
        <pre id="import-result" class="settings-result hidden"></pre>
      </div>
    </section>`
}

function bindSettings(root) {
  root.querySelector('#export-config')?.addEventListener('click', () => {
    if (!isWebAdmin()) return
    run(async () => {
      const res = await fetch('/admin/api/settings/backup', { credentials: 'same-origin' })
      if (res.status === 401) {
        location.href = '/admin/login'
        return
      }
      if (res.status === 403) {
        location.href = '/admin/denied?reason=not_authorized'
        return
      }
      if (!res.ok) {
        let msg = 'Export failed'
        try {
          const j = await res.json()
          if (j.error) msg = j.error
        } catch (_) {}
        throw new Error(msg)
      }
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'alfred-identity-config.json'
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
      showError('')
    })
  })
  root.querySelector('#import-config')?.addEventListener('click', () => {
    if (!isWebAdmin()) return
    root.querySelector('#config-file')?.click()
  })
  root.querySelector('#config-file')?.addEventListener('change', (ev) => {
    const file = ev.target.files && ev.target.files[0]
    ev.target.value = ''
    if (!file) return
    if (!confirm(`Import configuration from “${file.name}”? Existing accounts/groups/users with matching keys will be updated.`)) {
      return
    }
    run(async () => {
      const fd = new FormData()
      fd.append('file', file)
      const res = await api('/admin/api/settings/backup', { method: 'POST', body: fd })
      const lines = [
        `Users: ${res.users_added || 0} added, ${res.users_updated || 0} updated`,
        `Groups: ${res.groups_added || 0} added, ${res.groups_updated || 0} updated`,
        `Accounts: ${res.accounts_added || 0} added, ${res.accounts_updated || 0} updated`,
      ]
      if (res.errors && res.errors.length) {
        lines.push(`${res.errors.length} issue(s):`)
        lines.push(...res.errors.slice(0, 20).map((e) => `  · ${e}`))
        if (res.errors.length > 20) lines.push(`  …and ${res.errors.length - 20} more`)
        showError(`Import finished with ${res.errors.length} issue(s)`)
      } else {
        showError('')
      }
      const out = root.querySelector('#import-result')
      if (out) {
        out.textContent = lines.join('\n')
        out.classList.remove('hidden')
      }
    })
  })
}

function render() {
  renderTabs()
  const role = me?.web_role || (me?.is_admin ? 'admin' : 'readonly')
  $('#whoami').textContent = me
    ? `${me.display_name || me.discord_id} · ${webRoleLabel(role)}`
    : ''
  document.body.classList.toggle('readonly', !isWebAdmin())
  if (tab !== 'overview') destroyMetricCharts()
  const main = $('#main')
  if (connDurationTimer) {
    clearInterval(connDurationTimer)
    connDurationTimer = null
  }
  if (metricsRefreshTimer) {
    clearInterval(metricsRefreshTimer)
    metricsRefreshTimer = null
  }
  if (tab === 'overview') {
    bootstrapMetricsData()
    destroyMetricCharts()
    main.innerHTML = renderOverview()
    bindOverview(main)
    if (!metricsData?.since) loadMetrics()
    metricsRefreshTimer = setInterval(() => {
      if (tab === 'overview') loadMetrics()
    }, 30000)
    connDurationTimer = setInterval(() => {
      if (tab === 'overview') refreshOverviewDurations()
    }, 5000)
  } else if (tab === 'accounts') {
    main.innerHTML = renderAccounts()
    bindAccounts(main)
  } else if (tab === 'users') {
    main.innerHTML = renderUsers()
    bindUsers(main)
  } else if (tab === 'groups') {
    main.innerHTML = renderGroups()
    bindGroups(main)
  } else if (tab === 'shares') {
    main.innerHTML = renderShares()
    bindShares(main)
  } else if (tab === 'connections') {
    main.innerHTML = renderConnections()
    connDurationTimer = setInterval(() => {
      if (tab === 'connections') main.innerHTML = renderConnections()
    }, 5000)
  } else if (tab === 'audit') {
    main.innerHTML = renderAudit()
    bindAudit(main)
    if (!auditEntries.length && !auditLoading) loadAudit(true)
  } else if (tab === 'settings') {
    main.innerHTML = renderSettings()
    bindSettings(main)
  } else {
    main.innerHTML = renderSessions()
  }
  bindSortable(main)
}

async function boot() {
  applyTheme(readStoredTheme())
  const themeBtn = $('#theme-toggle')
  if (themeBtn) themeBtn.addEventListener('click', toggleTheme)
  const main = $('#main')
  if (main) {
    main.addEventListener('change', (e) => {
      if (e.target.id === 'metrics-range') {
        metricsRange = e.target.value
        loadMetrics()
      }
    })
  }
  me = await api('/admin/api/me')
  await refreshState(true)
  await loadMetrics({ updateOverview: false })
  connectLive()
  render()
}

boot().catch((e) => {
  showError(String(e.message || e))
  const msg = String(e.message || e)
  if (msg.includes('unauthorized')) {
    window.location.href = '/admin/login'
  } else if (msg.includes('forbidden') || msg.includes('access_revoked')) {
    window.location.href = '/admin/denied?reason=not_authorized'
  }
})
