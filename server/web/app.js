"use strict";

// ------------------------------------------------------------------ i18n
const LOG_ZH = [
  ["bad token on ", "令牌错误："],
  ["read-only AI token used on ", "AI 助手的只读令牌试图访问："],
  ["MCP token created in the app", "在 App 里为 AI 助手生成了令牌"],
  ["AI token created in dashboard", "在网页面板里为 AI 助手生成了令牌"],
  ["MCP token created", "为 AI 助手生成了令牌"],
  ["wrong or expired code", "配对码错误或已过期"],
  ["denied on the computer", "在电脑上被拒绝"],
  ["refused: not from the local network or Tailscale", "已拒绝：不是来自局域网或 Tailscale"],
  ["refused: ", "已拒绝："],
  ["too many failed attempts", "失败次数太多，暂时拒绝"],
];
const ZH = {
  "tab.overview": "概览", "tab.history": "同步记录", "tab.devices": "配对和设备", "tab.reports": "体检报告", "tab.mcp": "AI 助手",
  "st.running": "运行中", "toast.sync": "收到 {dev} 的 {n} 条新数据", "toast.paired": "已配对 {dev}",
  "ov.records": "健康记录", "ov.last": "最近同步", "ov.devices": "已配对设备", "ov.pairnew": "配对新的 iPhone →",
  "ov.reports": "体检报告", "ov.activity": "每天的记录", "ov.30d": "最近 30 天，按数据本身的日期", "ov.types": "数据类型", "ov.filter": "筛选",
  "ov.pick": "选择一个类型", "ov.pickhint": "点左边的数据类型，查看每天的数值。", "ov.server": "服务器",
  "col.type": "类型", "col.count": "条数", "col.latest": "最新", "col.time": "时间", "col.event": "事件", "col.device": "设备",
  "col.received": "收到", "col.new": "新增", "col.detail": "详情", "col.date": "日期",
  "k.all": "全部", "k.ingest": "数据", "k.report": "体检", "k.pair": "配对", "k.auth": "被拒绝", "k.hook": "同步后命令",
  "more": "加载更多", "pair.new": "配对新的 iPhone", "pair.title": "和「健康册」App 配对",
  "pair.s1": "点「配对新的 iPhone」，显示一次性配对码。",
  "pair.s2": "在 iPhone 上用相机扫二维码，或在 App → 添加目的地 → 本地服务器 里扫码。",
  "pair.s3": "App 会自动保存地址和自己的令牌，完成。",
  "pair.expires": "剩余", "pair.copy": "复制链接", "pair.cancel": "取消",
  "dev.title": "已配对设备", "dev.paired": "配对时间", "dev.seen": "最近同步", "dev.revoke": "撤销", "dev.revoked": "已撤销", "dev.delete": "删除", "dev.rename": "重命名", "dev.renameAsk": "新的名字（令牌不变，不用重新配对）",
  "dev.none": "还没有配对的设备。", "dev.confirm": "撤销后这台设备就不能再同步了，需要重新配对。确定吗？",
  "tok.title": "共享令牌（手动配置用）", "tok.hint": "扫不了码时，可以把它粘贴到 App 里。配对的设备各有自己的令牌，可以在上面单独撤销。",
  "tok.show": "显示", "tok.hide": "隐藏",
  "rep.hint": "在 App 里导入的体检报告（PDF / 照片 → 结构化数值）。原件保存在数据目录的 reports 文件夹里。",
  "rep.inst": "机构", "rep.items": "项目", "rep.flagged": "异常", "rep.received": "收到时间", "rep.none": "还没有体检报告。",
  "pend.title": "有设备请求配对", "pend.allow": "允许", "pend.deny": "拒绝", "pend.from": "来自",
  "pend.confirm": "手机上显示的确认码一致时再允许",
  "ev.ingest": "同步数据", "ev.report": "体检报告", "ev.pair": "配对", "ev.auth": "被拒绝", "ev.hook": "同步后命令", "ev.backup": "备份",
  "rd.item": "项目", "rd.value": "结果", "rd.range": "参考范围", "rd.pick": "点左边的项目，在原件里找到它。",
  "rd.files": "{n} 个原件", "rd.nofiles": "服务器上没有这份报告的原件（App 备份时要勾选「原始报告文件」）。",
  "rd.noloc": "这个项目没有位置信息（较早导入的报告没有）。", "rd.missing": "原件 {name} 不在服务器上。",
  "rd.page": "第 {n} 页", "rd.cantshow": "浏览器显示不了这个文件：", "rd.locate": "在原件里定位",
  "st.title": "存储空间", "st.total": "共 {n}", "st.disk": "磁盘剩余 {free} / {size}", "st.files": "{n} 个文件",
  "st.database": "健康数据库", "st.reports": "体检报告", "st.chats": "AI 对话备份", "st.settings": "App 设置备份", "st.csv": "CSV 文件", "st.other": "其它",
  "ok": "成功", "fail": "失败", "never": "从未", "none": "还没有记录。", "types": "种类型",
  "srv.address": "手机连接地址", "srv.data": "数据目录", "srv.outputs": "存储", "srv.version": "版本", "srv.started": "启动于",
  "srv.pairing": "配对方式", "srv.password": "控制面板密码", "yes": "已设置", "no": "未设置（仅局域网可访问）",
  "ago.s": "刚刚", "ago.m": "{n} 分钟前", "ago.h": "{n} 小时前", "ago.d": "{n} 天前", "copied": "已复制",
  "unit.records": "条", "dev.shared": "共享令牌", "copy": "复制",
  "mcp.title": "AI 助手（MCP）", "mcp.hint": "让 Claude 等支持 MCP 的 AI 助手读取你的健康数据（只读）。给它单独建一个令牌，和设备一样可以在「配对和设备」里撤销。",
  "mcp.create": "为 AI 助手创建令牌", "mcp.once": "现在复制，之后不会再显示。", "mcp.other": "其它客户端（Streamable HTTP）",
  "mcp.local": "在这台电脑上，桌面 App 也可以直接通过 stdio 运行，不需要令牌：",
  "mcp.on": "允许 AI 助手", "mcp.offHint": "已关闭：所有 AI 助手都连不上。令牌保留，重新打开就能用。",
  "mcp.extras": "连上后，AI 助手里可以直接选这些提示词：每周健康回顾、体检报告解读、睡眠分析、两个时间段对比；也能读取数据库结构、最新体检、近 7 天概况等资源。",
  "mcp.skill": "Skill 告诉 AI 助手怎么查你的数据。用下面的命令装到 Claude Code：",
};
const lang = (navigator.language || "en").toLowerCase().startsWith("zh") ? "zh" : "en";
const EN_FALLBACK = {
  "dev.revoke": "Revoke", "dev.revoked": "Revoked", "dev.delete": "Delete", "dev.rename": "Rename", "dev.renameAsk": "New name (the token stays; no need to pair again)","dev.none": "No paired devices yet.",
  "dev.confirm": "This device won't be able to sync any more until it pairs again. Revoke?", "tok.hide": "Hide",
  "rep.none": "No checkup reports yet.", "pend.title": "A device wants to pair", "pend.allow": "Allow", "pend.deny": "Deny", "pend.from": "from",
  "pend.confirm": "Allow only if the phone shows the same code",
  "ev.ingest": "Data", "ev.report": "Checkup", "ev.pair": "Pairing", "ev.auth": "Rejected", "ev.hook": "Hook", "ev.backup": "Backup",
  "rd.item": "Item", "rd.value": "Result", "rd.range": "Range", "rd.pick": "Click an item to find it in the original.",
  "rd.files": "{n} original(s)", "rd.nofiles": "No original of this report on the server (in the app's backup, turn on Original report files).",
  "rd.noloc": "This item has no location (reports imported earlier don't).", "rd.missing": "The original {name} is not on the server.",
  "rd.page": "page {n}", "rd.cantshow": "The browser can't show this file:", "rd.locate": "Find in the original",
  "st.title": "Storage", "st.total": "{n} in all", "st.disk": "{free} free of {size}", "st.files": "{n} files",
  "st.database": "Health database", "st.reports": "Checkups", "st.chats": "AI chat backups", "st.settings": "App settings backups", "st.csv": "CSV files", "st.other": "Other",
  "ok": "OK", "fail": "Failed", "never": "Never", "none": "Nothing yet.", "types": "types",
  "srv.address": "Address for phones", "srv.data": "Data directory", "srv.outputs": "Storage", "srv.version": "Version", "srv.started": "Started",
  "srv.pairing": "Pairing", "srv.password": "Dashboard password", "yes": "set", "no": "not set (LAN only)",
  "toast.sync": "{n} new records from {dev}", "toast.paired": "{dev} is paired",
  "ago.s": "just now", "ago.m": "{n} min ago", "ago.h": "{n} h ago", "ago.d": "{n} d ago", "copied": "Copied", "unit.records": "", "dev.shared": "shared token", "copy": "Copy",
};
function t(key, vars) {
  let s = (lang === "zh" ? ZH[key] : EN_FALLBACK[key]) ?? document.querySelector(`[data-i18n="${key}"]`)?.dataset.en ?? key;
  for (const [k, v] of Object.entries(vars || {})) s = s.replace(`{${k}}`, v);
  return s;
}
document.documentElement.lang = lang === "zh" ? "zh-CN" : "en";
document.querySelectorAll("[data-i18n]").forEach(el => {
  el.dataset.en = el.textContent;
  if (lang === "zh" && ZH[el.dataset.i18n]) el.textContent = ZH[el.dataset.i18n];
});
document.querySelectorAll("[data-i18n-ph]").forEach(el => { if (lang === "zh") el.placeholder = ZH[el.dataset.i18nPh] || el.placeholder; });

// ------------------------------------------------------------------ helpers
const $ = s => document.querySelector(s);
const esc = s => String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const nf = new Intl.NumberFormat(lang === "zh" ? "zh-CN" : "en");
const fmtNum = n => nf.format(Math.round(n * 100) / 100);
const fmtAxis = n => nf.format(n >= 100 ? Math.round(n) : Math.round(n * 10) / 10);
const devName = d => d === "shared token" ? t("dev.shared") : d;

async function api(path, opts = {}) {
  const r = await fetch(path, { ...opts, headers: { "X-HealthSync": "1", "Content-Type": "application/json", ...(opts.headers || {}) } });
  const body = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(body.error || r.statusText);
  return body;
}

function ago(iso) {
  if (!iso) return t("never");
  const s = (Date.now() - new Date(iso)) / 1000;
  if (s < 60) return t("ago.s");
  if (s < 3600) return t("ago.m", { n: Math.floor(s / 60) });
  if (s < 86400) return t("ago.h", { n: Math.floor(s / 3600) });
  return t("ago.d", { n: Math.floor(s / 86400) });
}
function when(iso) {
  if (!iso) return "–";
  const d = new Date(iso);
  if (isNaN(d)) return esc(iso);
  return d.toLocaleString(lang === "zh" ? "zh-CN" : undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}
function prettyType(type) {
  return type.replace(/^HK(Quantity|Category|Correlation|Data)TypeIdentifier/, "").replace(/^HKWorkoutTypeIdentifier$/, "Workout");
}

// ------------------------------------------------------------------ tiny SVG bar / line charts
function barChart(el, points, { label, value, fail, fmt = fmtNum }) {
  if (!points.length) { el.innerHTML = `<p class="empty">${t("none")}</p>`; return; }
  const W = el.clientWidth || 600, H = el.clientHeight || 160, padL = 40, padB = 18, padT = 6;
  const max = Math.max(1, ...points.map(value));
  const bw = (W - padL) / points.length;
  const y = v => padT + (H - padT - padB) * (1 - v / max);
  // Bars grow in when the chart first shows or shows other days; on a refresh only the
  // bars whose value changed grow (a sync adding to today), the rest stay still.
  const prev = el._vals || null, labels = points.map(label).join();
  const fresh = !prev || el._labels !== labels;
  el._vals = Object.fromEntries(points.map(p => [label(p), value(p)])); el._labels = labels;
  let svg = `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="none">`;
  for (const g of [0, .5, 1]) svg += `<line class="grid" x1="${padL}" x2="${W}" y1="${y(max * g)}" y2="${y(max * g)}"/><text class="axis" x="${padL - 6}" y="${y(max * g) + 3}" text-anchor="end">${fmtAxis(max * g)}</text>`;
  points.forEach((p, i) => {
    // a few days of data must not make a few huge slabs: bars are at most 28 wide
    const w = Math.min(bw * .7, 28), v = value(p), x = padL + i * bw + (bw - w) / 2, h = Math.max(v ? 2 : 0, H - padB - y(v));
    const grow = fresh ? ` grow" style="animation-delay:${Math.min(i * 12, 360)}ms` : prev[label(p)] !== v ? " grow" : "";
    svg += `<rect class="bar${fail && fail(p) && !v ? " fail" : ""}${grow}" x="${x}" y="${H - padB - h}" width="${w}" height="${fail && fail(p) && !v ? 2 : h}" rx="${Math.min(4, w / 4)}" data-tip="${esc(label(p))}: ${esc(fmt(v))}"/>`;
    if (i % Math.ceil(points.length / 6) === 0) svg += `<text class="axis" x="${x + w / 2}" y="${H - 4}" text-anchor="middle">${esc(label(p).slice(5))}</text>`;
  });
  el.innerHTML = svg + "</svg>";
  attachTips(el);
}
function attachTips(el) {
  let tip = el.querySelector(".tip");
  el.querySelectorAll("[data-tip]").forEach(r => {
    r.addEventListener("mouseenter", () => {
      if (!tip) { tip = document.createElement("div"); tip.className = "tip"; el.appendChild(tip); }
      const b = r.getBoundingClientRect(), eb = el.getBoundingClientRect();
      tip.textContent = r.dataset.tip; tip.style.left = (b.left - eb.left + b.width / 2) + "px"; tip.style.top = (b.top - eb.top) + "px"; tip.hidden = false;
    });
    r.addEventListener("mouseleave", () => tip && (tip.hidden = true));
  });
}

// ------------------------------------------------------------------ tabs
const tabs = ["overview", "history", "devices", "mcp", "reports"];
function show(tab) {
  tab = tab.split("/")[0]; // #reports/<id>/<item> opens a report
  if (!tabs.includes(tab)) tab = "overview";
  for (const name of tabs) $(`#tab-${name}`).hidden = name !== tab;
  document.querySelectorAll(".tabs button").forEach(b => b.setAttribute("aria-selected", b.dataset.tab === tab));
  ({ overview: loadOverview, history: () => loadLogs(true), devices: loadPairing, mcp: loadMCP, reports: loadReports })[tab]();
  current = tab;
}

// ------------------------------------------------------------------ header, live status, toasts
let lastSyncKey = null, knownDevices = null;
function applyHeader(o) {
  $("#server-name").textContent = o.name;
  $("#server-sub").textContent = o.base_url.replace(/^https?:\/\//, "");
  $("#ver").textContent = o.version;
  document.title = `${o.name} · Healthfolio`;
  // a new sync makes the status light pulse once and says so
  const s = o.last_sync;
  const key = s ? `${s.time}/${s.received}/${s.added}` : "";
  if (lastSyncKey !== null && key && key !== lastSyncKey && Date.now() - new Date(s.time) < 120000) {
    pulse($("#live"));
    if (s.added > 0) toast(t("toast.sync", { dev: devName(s.device || "iPhone"), n: fmtNum(s.added) }));
  }
  lastSyncKey = key;
}
function pulse(el) {
  el.classList.remove("pulse");
  void el.offsetWidth; // restart the animation
  el.classList.add("pulse");
}
let toastTimer = 0;
function toast(msg) {
  const el = $("#toast");
  el.textContent = msg; el.hidden = false;
  requestAnimationFrame(() => el.classList.add("on"));
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.classList.remove("on"); setTimeout(() => { el.hidden = true; }, 300); }, 4000);
}
// countTo rolls a number tile to its new value (not on the first load, and not for people
// who asked for less motion).
const lessMotion = matchMedia("(prefers-reduced-motion: reduce)");
function countTo(el, to, fmt = fmtNum) {
  const from = +el.dataset.n;
  el.dataset.n = to;
  if (!Number.isFinite(from) || from === to || lessMotion.matches || document.hidden) { el.textContent = fmt(to); return; }
  const start = performance.now(), dur = 700;
  const step = now => {
    const p = Math.min(1, (now - start) / dur), e = 1 - Math.pow(1 - p, 3);
    el.textContent = fmt(from + (to - from) * e);
    if (p < 1 && +el.dataset.n === to) requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
  el.classList.remove("bump"); void el.offsetWidth; el.classList.add("bump");
}
let current = "overview";
document.querySelectorAll(".tabs button").forEach(b => b.addEventListener("click", () => { location.hash = b.dataset.tab; }));
addEventListener("hashchange", () => show(location.hash.slice(1)));

// ------------------------------------------------------------------ overview
let types = [], selType = null;
async function loadOverview() {
  const o = await api("/api/overview");
  applyHeader(o);
  countTo($("#t-total"), o.stats.total);
  $("#t-types").textContent = `${o.stats.types.length} ${t("types")}`;
  const last = o.last_sync;
  $("#t-last").textContent = last ? ago(last.time) : t("never");
  $("#t-last-hint").textContent = last ? `${devName(last.device || "")} · ${last.ok ? "+" + fmtNum(last.added) : t("fail")}` : "";
  countTo($("#t-devices"), o.devices);
  countTo($("#t-reports"), o.reports);
  document.querySelectorAll(".tile").forEach(el => el.classList.remove("loading"));
  barChart($("#activity"), o.activity, { label: p => p.day, value: p => p.records, fail: p => p.fails > 0 });
  types = o.stats.types;
  renderTypes();
  // show the busiest type straight away, rather than an empty "pick a type"
  if (!selType && types.length) { selType = types[0].type; renderTypes(); loadDaily(); }
  const outputs = o.outputs.map(x => `${esc(x.kind)}: <code>${esc(x.where)}</code>`).join("<br>");
  $("#server-info").innerHTML = [
    [t("srv.address"), `<code>${esc(o.base_url)}</code>`],
    [t("srv.data"), `<code>${esc(o.data_dir)}</code>`],
    [t("srv.outputs"), outputs],
    [t("srv.pairing"), esc(o.pair_modes.join(" / "))],
    [t("srv.password"), o.password ? t("yes") : t("no")],
    [t("srv.version"), `${esc(o.version)} · ${esc(o.os)} · protocol ${o.protocol}`],
    [t("srv.started"), when(o.started)],
  ].map(([k, v]) => `<dt>${k}</dt><dd>${v}</dd>`).join("");
  renderStorage(o.storage);
}
const ST_COLORS = ["var(--accent)", "#3fb27f", "#5b8def", "#c08be8", "#e0a243", "var(--muted)"];
function fmtBytes(n) {
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1000 && i < u.length - 1) { n /= 1000; i++; }
  return (i === 0 || n >= 100 ? n.toFixed(0) : n.toFixed(1)) + " " + u[i];
}
function renderStorage(st) {
  const parts = (st && st.parts) || [];
  $("#storage-card").hidden = parts.length === 0;
  if (!parts.length) return;
  let sub = t("st.total").replace("{n}", fmtBytes(st.total));
  if (st.disk_size) sub += " · " + t("st.disk").replace("{free}", fmtBytes(st.disk_free)).replace("{size}", fmtBytes(st.disk_size));
  $("#storage-sub").textContent = sub;
  $("#storage-bar").innerHTML = parts.filter(p => p.bytes > 0).map((p, i) =>
    `<span style="flex:${p.bytes / st.total};background:${ST_COLORS[Math.min(parts.indexOf(p), ST_COLORS.length - 1)]}" title="${esc(t("st." + p.kind))}"></span>`).join("");
  $("#storage-rows").innerHTML = parts.map((p, i) =>
    `<tr><td class="nowrap"><i class="dot" style="background:${ST_COLORS[Math.min(i, ST_COLORS.length - 1)]}"></i>${esc(t("st." + p.kind))}</td>
      <td class="path" title="${esc(p.path)}"><code>${esc(p.path)}</code></td>
      <td class="num muted nowrap">${p.files === 1 && lang !== "zh" ? "1 file" : t("st.files").replace("{n}", fmtNum(p.files))}</td><td class="num nowrap"><b>${fmtBytes(p.bytes)}</b></td></tr>`).join("");
}
function renderTypes() {
  const q = $("#type-filter").value.toLowerCase();
  const rows = types.filter(x => x.type.toLowerCase().includes(q));
  $("#types tbody").innerHTML = rows.length ? rows.map(x =>
    `<tr data-type="${esc(x.type)}" class="${x.type === selType ? "sel" : ""}"><td title="${esc(x.type)}">${esc(prettyType(x.type))}</td><td class="num">${fmtNum(x.count)}</td><td class="nowrap">${esc((x.latest || "").slice(0, 10))}</td></tr>`
  ).join("") : `<tr><td colspan="3" class="empty">${t("none")}</td></tr>`;
}
$("#type-filter").addEventListener("input", renderTypes);
$("#types tbody").addEventListener("click", e => {
  const tr = e.target.closest("tr[data-type]");
  if (tr) { selType = tr.dataset.type; renderTypes(); loadDaily(); }
});
$("#td-days").addEventListener("change", loadDaily);
async function loadDaily() {
  if (!selType) return;
  $("#td-title").textContent = prettyType(selType);
  const pts = await api(`/api/daily?type=${encodeURIComponent(selType)}&days=${$("#td-days").value}`);
  // cumulative types (steps, energy, distance) read best as daily totals, the rest as averages
  const cumulative = /Count|Distance|Energy|Flights|Time|Duration|Steps/i.test(selType);
  const unit = pts[0]?.unit || "";
  $("#td-title").textContent = prettyType(selType) + (unit ? ` (${unit})` : "");
  barChart($("#td-chart"), pts, { label: p => p.day, value: p => cumulative ? p.total : p.mean, fmt: v => fmtNum(v) + (unit ? " " + unit : "") });
}

// ------------------------------------------------------------------ history
let logKind = "", oldest = 0;
async function loadLogs(reset) {
  if (reset) oldest = 0;
  const q = new URLSearchParams({ limit: 100, kind: logKind });
  if (oldest) q.set("before", oldest);
  const rows = await api("/api/logs?" + q);
  // the server's own messages are stored in English
  const logMessage = m => {
    if (lang !== "zh") return m;
    for (const [en, zh] of LOG_ZH) if (m.startsWith(en)) return zh + m.slice(en.length);
    return m;
  };
  if (rows.length) oldest = rows[rows.length - 1].id;
  const html = rows.map(r => {
    const status = r.ok ? `<span class="badge ok">${t("ok")}</span>` : `<span class="badge bad">${t("fail")}</span>`;
    let detail = esc(logMessage(r.message || ""));
    if (r.types && r.types.length) detail = `<details><summary>${r.types.length} ${t("types")}</summary><pre>${esc(r.types.map(prettyType).join("\n"))}</pre></details>` + detail;
    if (r.kind === "hook" && r.message) detail = `<details><summary>output</summary><pre>${esc(r.message)}</pre></details>`;
    const meta = [r.ip, r.duration_ms ? r.duration_ms + " ms" : "", r.bytes ? fmtNum(r.bytes / 1024) + " KB" : ""].filter(Boolean).join(" · ");
    return `<tr><td class="nowrap" title="${esc(r.time)}">${when(r.time)}</td><td class="nowrap">${status} ${esc(t("ev." + r.kind))}</td>
      <td>${esc(devName(r.device))}<div class="muted detail">${esc(meta)}</div></td><td class="num">${r.received ? fmtNum(r.received) : ""}</td>
      <td class="num">${r.kind === "ingest" || r.kind === "report" ? fmtNum(r.added) : ""}</td><td class="detail">${detail}</td></tr>`;
  }).join("");
  const tb = $("#logs tbody");
  if (reset) tb.innerHTML = html || `<tr><td colspan="6" class="empty">${t("none")}</td></tr>`;
  else tb.insertAdjacentHTML("beforeend", html);
  $("#more").hidden = rows.length < 100;
}
$("#kind-filter").addEventListener("click", e => {
  const b = e.target.closest("button"); if (!b) return;
  document.querySelectorAll("#kind-filter button").forEach(x => x.classList.toggle("on", x === b));
  logKind = b.dataset.kind; loadLogs(true);
});
$("#more").addEventListener("click", () => loadLogs(false));

// ------------------------------------------------------------------ pairing & devices
let pairState = null, countdown = null, mcpConfigs = [];
function renderMCP(on) {
  $("#mcp-on").checked = on;
  $("#mcp-off").hidden = on;
  $("#mcp-body").hidden = !on;
  $("#skill-cmd").textContent = `mkdir -p ~/.claude/skills/healthsync && curl -fsSL ${location.origin}/skill.md -o ~/.claude/skills/healthsync/SKILL.md`;
}
$("#mcp-on").addEventListener("change", async e => {
  const on = e.target.checked;
  try { await api("/api/mcp", { method: "POST", body: JSON.stringify({ enabled: on }) }); renderMCP(on); }
  catch (err) { e.target.checked = !on; alert(err.message || err); }
});
async function loadMCP() {
  const o = await api("/api/overview");
  applyHeader(o);
  renderMCP(o.mcp_enabled);
}
async function loadPairing() {
  const [p, devs] = await Promise.all([api("/api/pair"), api("/api/devices")]);
  renderPair(p);
  noticeDevices(devs);
  $("#devices tbody").innerHTML = devs.length ? devs.map(d => `<tr>
      <td>${esc(d.name)}${d.revoked ? ` <span class="badge bad">${t("dev.revoked")}</span>` : ""}<div class="muted detail">${esc(d.last_ip)}</div></td>
      <td class="nowrap">${when(d.created)}</td><td class="nowrap">${d.last_seen ? ago(d.last_seen) : "–"}</td>
      <td class="num nowrap"><button class="ghost" data-rename="${d.id}" data-name="${esc(d.name)}">${t("dev.rename")}</button> ${d.revoked
        ? `<button class="ghost danger" data-del="${d.id}">${t("dev.delete")}</button>`
        : `<button class="ghost danger" data-revoke="${d.id}">${t("dev.revoke")}</button>`}</td></tr>`).join("")
    : `<tr><td colspan="4" class="empty">${t("dev.none")}</td></tr>`;
}
// noticeDevices says when a phone has just paired (not on the first look).
function noticeDevices(devs) {
  const ids = new Set(devs.map(d => d.id));
  if (knownDevices) for (const d of devs) if (!knownDevices.has(d.id) && d.scope !== "mcp") toast(t("toast.paired", { dev: d.name }));
  knownDevices = ids;
}
function renderPair(p) {
  pairState = p.active || null;
  const pend = p.pending || [];
  const pc = $("#pending-count");
  pc.hidden = !pend.length; pc.textContent = pend.length;
  $("#pending").innerHTML = pend.map(x => `<div class="card pending"><div class="row">
      <div><h2>${t("pend.title")}</h2><p class="muted">${esc(x.device)} · ${t("pend.from")} ${esc(x.ip)}</p>${x.confirm ? `<p class="muted">${t("pend.confirm")}</p>` : ""}</div>
      ${x.confirm ? `<div class="confirm-code">${esc(x.confirm)}</div>` : ""}
      <div><button class="ghost" data-answer="${esc(x.id)}" data-allow="0">${t("pend.deny")}</button> <button class="primary" data-answer="${esc(x.id)}" data-allow="1">${t("pend.allow")}</button></div>
    </div></div>`).join("");
  if (pairState) {
    $("#qr").innerHTML = pairState.qr;
    $("#pair-active").hidden = false;
    $("#pair-code").textContent = pairState.code.slice(0, 3) + " " + pairState.code.slice(3);
    $("#pair-url").textContent = pairState.url;
    tick();
    clearInterval(countdown); countdown = setInterval(tick, 1000);
  } else {
    clearInterval(countdown);
    $("#pair-active").hidden = true;
    $("#qr").innerHTML = `<button class="primary" id="pair-btn">${t("pair.new")}</button>`;
  }
}
function tick() {
  if (!pairState) return;
  const left = Math.max(0, pairState.expires - Date.now() / 1000);
  $("#pair-left").textContent = `${Math.floor(left / 60)}:${String(Math.floor(left % 60)).padStart(2, "0")}`;
  if (left <= 0) loadPairing();
}
document.addEventListener("click", async e => {
  const b = e.target.closest("button"); if (!b) return;
  try {
    if (b.id === "pair-btn") renderPair(await api("/api/pair", { method: "POST" }));
    else if (b.id === "pair-cancel") { await api("/api/pair", { method: "DELETE" }); loadPairing(); }
    else if (b.id === "pair-copy") { await navigator.clipboard.writeText(pairState.link); b.textContent = t("copied"); }
    else if (b.dataset.revoke) { if (confirm(t("dev.confirm"))) { await api(`/api/devices/${b.dataset.revoke}/revoke`, { method: "POST" }); loadPairing(); } }
    else if (b.dataset.rename) {
      const name = prompt(t("dev.renameAsk"), b.dataset.name);
      if (name && name.trim() && name.trim() !== b.dataset.name) {
        await api(`/api/devices/${b.dataset.rename}`, { method: "PATCH", body: JSON.stringify({ name: name.trim() }) });
        loadPairing();
      }
    }
    else if (b.dataset.del) { await api(`/api/devices/${b.dataset.del}`, { method: "DELETE" }); loadPairing(); }
    else if (b.dataset.answer) { await api(`/api/pair/pending/${b.dataset.answer}`, { method: "POST", body: JSON.stringify({ allow: b.dataset.allow === "1" }) }); setTimeout(loadPairing, 500); }
    else if (b.id === "mcp-token") {
      const r = await api("/api/devices", { method: "POST", body: JSON.stringify({ name: "AI assistant" }) });
      mcpConfigs = r.config_list;
      $("#mcp-configs").innerHTML = r.config_list.map((c, i) => `<div class="cfg"><div class="cfg-head"><span>${esc(c.title)}</span>
        <button class="ghost" data-copy="${i}">${t("copy")}</button></div><pre class="snippet">${esc(c.text)}</pre></div>`).join("");
      $("#mcp-setup").hidden = false; loadPairing();
    }
    else if (b.dataset.copy) { await navigator.clipboard.writeText(mcpConfigs[+b.dataset.copy].text); b.textContent = t("copied"); }
    else if (b.id === "token-show") {
      const code = $("#token");
      if (code.dataset.shown) { code.textContent = "••••••••"; delete code.dataset.shown; b.textContent = t("tok.show"); }
      else {
        try { code.textContent = (await api("/api/token")).token; }
        catch (e) { code.textContent = e.message; }
        code.dataset.shown = 1; b.textContent = t("tok.hide");
      }
    }
  } catch (err) { alert(err.message); }
});

// ------------------------------------------------------------------ reports
async function loadReports() {
  const reps = await api("/api/reports");
  $("#reports tbody").innerHTML = reps.length ? reps.map(r => `<tr data-report="${esc(r.id)}" class="${r.id === repSel ? "sel" : ""}"><td class="nowrap">${esc(r.date)}</td><td>${esc(r.title || r.institution)}</td>
    <td class="num">${r.items}</td><td class="num">${r.flagged ? `<span class="badge bad">${r.flagged}</span>` : "0"}</td><td class="nowrap">${when(r.received)}</td></tr>`).join("")
    : `<tr><td colspan="5" class="empty">${t("rep.none")}</td></tr>`;
  // #reports/<id>/<item>: open a report (and an item) straight away, as the desktop app links
  const m = location.hash.match(/^#reports\/([^/]+)(?:\/(\d+))?/);
  if (m && !repDetail) openReport(decodeURIComponent(m[1]), m[2] !== undefined ? +m[2] : null);
}

let repSel = null, repDetail = null, pdfLib = null;
const pdfDocs = {};
async function openReport(id, item) {
  repSel = id;
  document.querySelectorAll("#reports tbody tr").forEach(tr => tr.classList.toggle("sel", tr.dataset.report === id));
  const d = await api("/api/reports/" + encodeURIComponent(id));
  repDetail = d;
  const r = d.report;
  $("#report-detail").hidden = false;
  $("#rd-title").textContent = r.title || r.institution || r.date;
  $("#rd-sub").textContent = [r.date, r.title ? r.institution : "", t("rd.files", { n: d.files.length })].filter(Boolean).join(" · ");
  $("#rd-items tbody").innerHTML = r.items.map((it, i) => {
    const bad = /^(high|low|h|l|abnormal)$/i.test(it.flag);
    const orig = it.originalName && it.originalName !== it.name ? `<div class="muted small">${esc(it.originalName)}</div>` : "";
    return `<tr data-item="${i}" class="${it.location ? "locatable" : ""}"><td>${esc(it.name)}${orig}</td>
      <td class="num nowrap">${bad ? `<b class="bad-text">${esc(it.value)}</b>` : esc(it.value)} <span class="muted">${esc(it.unit)}</span></td>
      <td class="muted nowrap">${esc(it.range)}</td><td class="nowrap">${it.location ? `<span class="loc" title="${esc(t("rd.locate"))}">⌖</span>` : ""}</td></tr>`;
  }).join("") || `<tr><td colspan="4" class="empty">${t("none")}</td></tr>`;
  $("#rd-files").innerHTML = d.files.map(f => `<a href="${fileURL(id, f.name)}" target="_blank" rel="noopener">${esc(f.name)}</a>`).join(" · ") ||
    `<span class="muted">${t("rd.nofiles")}</span>`;
  $("#rd-page").innerHTML = `<p class="empty">${t(d.files.length ? "rd.pick" : "rd.nofiles")}</p>`;
  $("#report-detail").scrollIntoView({ behavior: "smooth", block: "start" });
  if (item != null && r.items[item]) showItem(item);
}
const fileURL = (id, name) => `/api/reports/${encodeURIComponent(id)}/files/${encodeURIComponent(name)}`;

// showItem shows the page of the original the item is on, with its row framed.
async function showItem(i) {
  const it = repDetail.report.items[i];
  document.querySelectorAll("#rd-items tbody tr").forEach(tr => tr.classList.toggle("sel", +tr.dataset.item === i));
  history.replaceState(null, "", `#reports/${encodeURIComponent(repDetail.report.id)}/${i}`);
  const box = $("#rd-page");
  // the location comes from the phone as sent: only numbers go into styles and URLs
  const num = v => (Number.isFinite(+v) ? +v : 0);
  const loc = it.location && { file: String(it.location.file ?? ""), page: Math.max(0, Math.floor(num(it.location.page))),
    x: num(it.location.x), y: num(it.location.y), w: num(it.location.w), h: num(it.location.h) };
  if (!loc) { box.innerHTML = `<p class="empty">${t("rd.noloc")}</p>`; return; }
  const file = repDetail.files.find(f => f.name === loc.file);
  if (!file) { box.innerHTML = `<p class="empty">${t("rd.missing", { name: esc(loc.file) })}</p>`; return; }
  const url = fileURL(repDetail.report.id, file.name);
  const frame = `<div class="rd-hl" style="left:${loc.x * 100}%;top:${loc.y * 100}%;width:${loc.w * 100}%;height:${loc.h * 100}%"></div>`;
  const caption = `<p class="muted small">${esc(file.name)}${file.type === "application/pdf" ? " · " + t("rd.page", { n: loc.page + 1 }) : ""}</p>`;
  if (file.type.startsWith("image/")) {
    box.innerHTML = `${caption}<div class="rd-sheet"><img src="${url}" alt="">${frame}</div>`;
    const img = box.querySelector("img");
    img.onload = () => box.querySelector(".rd-hl").scrollIntoView({ block: "center", behavior: "smooth" });
    img.onerror = () => { box.innerHTML = `<p class="empty">${t("rd.cantshow")} <a href="${url}" target="_blank">${esc(file.name)}</a></p>`; };
    return;
  }
  if (file.type !== "application/pdf") { box.innerHTML = `<p class="empty"><a href="${url}" target="_blank">${esc(file.name)}</a></p>`; return; }
  box.innerHTML = `${caption}<p class="empty">…</p>`;
  try {
    pdfLib = pdfLib || await loadPDFJS();
    const doc = pdfDocs[url] || (pdfDocs[url] = await pdfLib.getDocument({ url, isEvalSupported: false }).promise);
    const page = await doc.getPage(loc.page + 1);
    const vp1 = page.getViewport({ scale: 1 });
    const scale = Math.min(3, Math.max(1, (box.clientWidth * devicePixelRatio) / vp1.width));
    const vp = page.getViewport({ scale });
    const canvas = document.createElement("canvas");
    canvas.width = vp.width; canvas.height = vp.height;
    await page.render({ canvasContext: canvas.getContext("2d"), viewport: vp }).promise;
    box.innerHTML = `${caption}<div class="rd-sheet">${frame}</div>`;
    box.querySelector(".rd-sheet").prepend(canvas);
    box.querySelector(".rd-hl").scrollIntoView({ block: "center", behavior: "smooth" });
  } catch (err) {
    // no pdf.js (offline): the browser's own viewer, at the page
    box.innerHTML = `${caption}<iframe class="rd-pdf" src="${url}#page=${loc.page + 1}"></iframe>`;
  }
}

// pdf.js draws the PDF page, so the frame can sit on the item's row.
// pdf.js 3.11 runs font code through eval unless told not to (CVE-2024-4367): getDocument
// passes isEvalSupported: false. Both files are checked against their known hashes.
const PDFJS = "https://cdnjs.cloudflare.com/ajax/libs/pdf.js/3.11.174/";
const PDFJS_SRI = "sha384-/1qUCSGwTur9vjf/z9lmu/eCUYbpOTgSjmpbMQZ1/CtX2v/WcAIKqRv+U1DUCG6e";
const PDFJS_WORKER_SRI = "sha384-SnzOobpRMLXZ52iJvZm/C0fYw0OQemTXzTjIsdsfMcrCtCEe9qgzxTd3RSklO5x2";
function loadPDFJS() {
  return new Promise((ok, fail) => {
    const s = document.createElement("script");
    s.src = PDFJS + "pdf.min.js";
    s.integrity = PDFJS_SRI;
    s.crossOrigin = "anonymous";
    s.onload = async () => {
      try {
        const res = await fetch(PDFJS + "pdf.worker.min.js", { integrity: PDFJS_WORKER_SRI, mode: "cors" });
        window.pdfjsLib.GlobalWorkerOptions.workerSrc = URL.createObjectURL(await res.blob());
        ok(window.pdfjsLib);
      } catch (err) { fail(err); }
    };
    s.onerror = fail;
    document.head.appendChild(s);
  });
}

document.addEventListener("click", e => {
  const rep = e.target.closest("#reports tbody tr[data-report]");
  if (rep) { openReport(rep.dataset.report, null).catch(err => alert(err.message)); return; }
  const item = e.target.closest("#rd-items tbody tr[data-item]");
  if (item) showItem(+item.dataset.item);
});

// ------------------------------------------------------------------ live refresh
setInterval(() => {
  if (document.hidden) return;
  // the overview is cached on the server until records change, so asking is cheap
  if (current === "overview") loadOverview().catch(() => {});
  else api("/api/overview").then(applyHeader).catch(() => {});
  api("/api/pair").then(p => {
    const pend = p.pending || [];
    if (current !== "devices") {
      // a phone asking to pair shows on the sidebar, wherever one is
      const pc = $("#pending-count"); pc.hidden = !pend.length; pc.textContent = pend.length;
      if (pend.length && !pc.dataset.seen) { pulse(pc); pc.dataset.seen = 1; } else if (!pend.length) delete pc.dataset.seen;
      return;
    }
    // only re-render when something changed, so the QR doesn't flicker
    const key = JSON.stringify([p.active?.code, pend.map(x => x.id)]);
    if (key !== renderPair.key) { renderPair.key = key; loadPairing().catch(() => {}); }
  }).catch(() => {});
}, 5000);
if (!location.hash.startsWith("#overview") && location.hash.length > 1) api("/api/overview").then(applyHeader).catch(() => {});
show(location.hash.slice(1));
