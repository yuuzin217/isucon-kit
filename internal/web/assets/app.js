"use strict";

const state = {
  runs: [],
  current: null,
  baseID: "",
  compareOff: false,
  mode: "remote",
  executionEnabled: false,
  pages: { api: 0, sql: 0 },
  historyScope: "mode",
};

const el = (id) => document.getElementById(id);

function text(node, value) {
  if (!node) return;
  node.textContent = value == null ? "" : String(value);
}

function showError(message) {
  const node = el("error");
  text(node, message || "");
  node.hidden = !message;
}

function csrf() {
  const match = document.cookie.match(/(?:^|; )isucon_csrf=([^;]+)/);
  return match ? decodeURIComponent(match[1]) : "";
}

async function getJSON(url) {
  const res = await fetch(url, { cache: "no-store" });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(body.error || ("取得に失敗しました (" + res.status + ")"));
  }
  return body;
}

async function postJSON(url, payload) {
  const res = await fetch(url, {
    method: "POST",
    cache: "no-store",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf() },
    body: JSON.stringify(payload || {}),
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(body.error || ("操作に失敗しました (" + res.status + ")"));
  }
  return body;
}

function scoreText(score) {
  return score == null ? "未計測" : String(score);
}

function statusText(status) {
  if (status === "pass") return "PASS";
  if (status === "fail") return "FAIL";
  return "未計測";
}

function modeText(mode) {
  if (mode === "local") return "ローカル練習";
  if (mode === "remote") return "リモート";
  return "過去の計測";
}

function seconds(n) {
  if (n == null || Number.isNaN(Number(n))) return "-";
  return Number(n).toFixed(3);
}

function totalLabel(n) {
  if (n == null || Number.isNaN(Number(n))) return "-";
  return Number(n).toFixed(3) + " 秒";
}

function avgLabel(n) {
  if (n == null || Number.isNaN(Number(n))) return "-";
  return (Number(n) * 1000).toFixed(1) + " ミリ秒";
}

function setVisible(id, on) {
  const node = el(id);
  if (node) node.hidden = !on;
}

function clearTable(id) {
  const body = el(id);
  if (!body) return;
  while (body.firstChild) body.removeChild(body.firstChild);
}

function addRow(id, cells, wrapIndex) {
  const tr = document.createElement("tr");
  cells.forEach((value, i) => {
    const td = document.createElement("td");
    if (i === wrapIndex) td.className = "wrap";
    if (typeof value === "object" && value && value.num) {
      td.className = (td.className + " num").trim();
      text(td, value.num);
    } else {
      text(td, value);
    }
    tr.appendChild(td);
  });
  el(id).appendChild(tr);
}

function sideLabel(value) {
  switch (value) {
    case "ok": return "解析済み";
    case "failed": return "解析失敗";
    case "partial": return "一部失敗";
    case "absent": return "未収集";
    case "pending": return "未解析";
    default: return value || "-";
  }
}

function visibleRuns() {
  if (state.historyScope === "all") return state.allRuns || state.runs;
  return (state.allRuns || state.runs).filter((run) => run.mode === state.mode);
}

function renderRuns() {
  const runs = state.historyScope === "all" ? (state.allRuns || []) : state.runs;
  const runSel = el("run-select");
  const baseSel = el("base-select");
  runSel.replaceChildren();
  baseSel.replaceChildren();
  const none = document.createElement("option");
  text(none, "比較しない");
  none.value = "";
  baseSel.appendChild(none);
  runs.forEach((run) => {
    const label = (run.created_at || "") + " " + (run.label || "(名称なし)") + " / " + scoreText(run.score) + " / " + modeText(run.mode);
    const a = document.createElement("option");
    a.value = run.id;
    text(a, label);
    runSel.appendChild(a);
    baseSel.appendChild(a.cloneNode(true));
  });
  if (state.current) runSel.value = state.current.run.id;
  baseSel.value = state.baseID || "";
  clearTable("history-body");
  runs.forEach((run) => {
    addRow("history-body", [run.created_at, run.label || "-", modeText(run.mode), { num: scoreText(run.score) }, statusText(run.benchmark_status)], 1);
  });
  clearTable("trend-body");
  const trend = runs.filter((run) => run.benchmark_status === "pass" && run.score != null).slice().reverse();
  trend.forEach((run) => {
    addRow("trend-body", [run.created_at, run.label || "-", { num: scoreText(run.score) }, statusText(run.benchmark_status)], 1);
  });
  const recent = el("recent");
  recent.replaceChildren();
  runs.slice(0, 5).forEach((run) => {
    const li = document.createElement("li");
    text(li, (run.label || "計測") + " / " + scoreText(run.score) + " / " + statusText(run.benchmark_status));
    recent.appendChild(li);
  });
}

const rankGen = { api: 0, sql: 0 };
state.rankTotal = { api: 0, sql: 0 };

function pageSizeOf(kind) {
  const limitSel = el(kind === "api" ? "api-limit" : "sql-limit").value;
  if (limitSel === "50") return 50;
  if (limitSel === "all") return 100;
  return 10;
}

async function renderRank(kind) {
  const detail = state.current;
  if (!detail) return;
  const gen = ++rankGen[kind];
  const isAPI = kind === "api";
  const analysis = isAPI ? detail.run.access_analysis : detail.run.slow_analysis;
  const stateId = isAPI ? "endpoint-state" : "query-state";
  const bodyId = isAPI ? "endpoint-body" : "query-body";
  const pager = el(isAPI ? "api-pager" : "sql-pager");
  clearTable(bodyId);
  if (analysis === "absent") {
    text(el(stateId), "未収集");
    pager.hidden = true;
    return;
  }
  if (analysis === "failed") {
    text(el(stateId), "解析失敗");
    pager.hidden = true;
    return;
  }
  if (analysis === "pending") {
    text(el(stateId), "未解析");
    pager.hidden = true;
    return;
  }
  const search = (el(isAPI ? "api-search" : "sql-search").value || "").trim();
  const key = el(isAPI ? "api-sort" : "sql-sort").value;
  const order = el(isAPI ? "api-order" : "sql-order").value;
  const pageSize = pageSizeOf(kind);
  const offset = state.pages[kind] * pageSize;
  const kindPath = isAPI ? "endpoints" : "queries";
  const data = await getJSON("/api/runs/" + encodeURIComponent(detail.run.id) + "/" + kindPath
    + "?sort=" + encodeURIComponent(key)
    + "&order=" + encodeURIComponent(order)
    + "&q=" + encodeURIComponent(search)
    + "&limit=" + pageSize
    + "&offset=" + offset);
  if (gen !== rankGen[kind] || !state.current || state.current.run.id !== detail.run.id) return;
  const total = data.total || 0;
  state.rankTotal[kind] = total;
  const view = data.items || [];
  const pageCount = Math.max(1, Math.ceil(total / pageSize));
  if (state.pages[kind] >= pageCount) state.pages[kind] = pageCount - 1;
  const orderLabel = key === "count" ? (order === "asc" ? "少ない順" : "多い順") : (order === "asc" ? "短い順" : "長い順");
  const keyLabel = key === "avg" ? "平均処理時間" : key === "count" ? "呼び出し回数" : "合計処理時間";
  if (!view.length) {
    text(el(stateId), "該当する行はありません");
  } else {
    const from = offset + 1;
    const to = offset + view.length;
    text(el(stateId), keyLabel + "の" + orderLabel + "（全" + total + "件中 " + from + "–" + to + "件）");
  }
  view.forEach((row) => {
    if (isAPI) {
      addRow(bodyId, [
        row.method || "",
        row.route || "",
        { num: String(row.count ?? 0) },
        { num: totalLabel(row.total_seconds) },
        { num: avgLabel(row.avg_seconds) },
      ], 1);
    } else {
      addRow(bodyId, [
        row.fingerprint || "",
        { num: String(row.count ?? 0) },
        { num: totalLabel(row.total_seconds) },
        { num: avgLabel(row.avg_seconds) },
      ], 0);
    }
  });
  pager.hidden = total <= pageSize;
  text(el(isAPI ? "api-page-label" : "sql-page-label"), (state.pages[kind] + 1) + " / " + pageCount);
}

async function renderDetail(detail) {
  const previous = state.current && state.current.run ? state.current.run.id : "";
  if (previous !== detail.run.id) state.pages = { api: 0, sql: 0 };
  state.current = detail;
  const run = detail.run;
  text(el("run-meta"), "選択中: " + (run.label || run.id) + " / " + run.created_at);
  text(el("score-time"), "計測日時: " + (run.created_at || "-"));
  text(el("log-range"), run.collection_scope ? "ログ範囲: " + run.collection_scope : "");
  text(el("score-value"), scoreText(run.score));
  text(el("score-status"), statusText(run.benchmark_status));
  text(el("score-note"), run.note ? "メモ: " + run.note : "");
  text(el("endpoint-state"), "");
  text(el("query-state"), "");
  await renderRank("api");
  await renderRank("sql");
  ["score", "analysis"].forEach((id) => setVisible(id, true));
  const empty = el("empty");
  if (empty) empty.hidden = true;
  loadHints(run.id);
}

function resetCompareTables() {
  clearTable("endpoint-diff-body");
  clearTable("query-diff-body");
  el("endpoint-diff-wrap").hidden = true;
  el("query-diff-wrap").hidden = true;
  text(el("endpoint-compare"), "");
  text(el("query-compare"), "");
  text(el("score-percent"), "-");
  text(el("score-base"), "-");
}

function showDiff(wrapId, bodyId, rows, cellsFor) {
  const list = (rows || []).slice(0, 10);
  clearTable(bodyId);
  if (list.length === 0) {
    el(wrapId).hidden = true;
    return;
  }
  el(wrapId).hidden = false;
  list.forEach((row) => addRow(bodyId, cellsFor(row), 0));
}

async function renderCompare() {
  resetCompareTables();
  if (!state.current || !state.baseID || state.baseID === state.current.run.id) {
    text(el("score-delta"), "-");
    return;
  }
  const cmp = await getJSON("/api/compare?base=" + encodeURIComponent(state.baseID) + "&head=" + encodeURIComponent(state.current.run.id));
  const pct = cmp.score_percent === "N/A" ? "N/A" : cmp.score_percent + "%";
  text(el("score-percent"), pct);
  if (cmp.score_delta == null) {
    text(el("score-delta"), "N/A");
  } else {
    text(el("score-delta"), String(cmp.score_delta));
  }
  const baseRun = (state.allRuns || state.runs).find((run) => run.id === cmp.base_id);
  text(el("score-base"), baseRun ? scoreText(baseRun.score) : "-");
  text(el("score-note"), "比較元: " + (baseRun ? scoreText(baseRun.score) + " / " + statusText(baseRun.benchmark_status) + " / " + (baseRun.created_at || "") : cmp.base_id) + (state.current.run.note ? " / メモ: " + state.current.run.note : ""));
  if (!cmp.endpoints_comparable) {
    text(el("endpoint-compare"), cmp.endpoints_reason || "エンドポイントは比較不能です");
  } else if ((cmp.endpoints || []).length === 0) {
    text(el("endpoint-compare"), "比較できるAPIの差分はありません");
  } else {
    text(el("endpoint-compare"), "APIの差分です。合計処理時間の差（秒）で、表示中から比較元を引いています。");
    showDiff("endpoint-diff-wrap", "endpoint-diff-body", cmp.endpoints, (row) => [
      row.method || "",
      row.route || "",
      { num: seconds(row.base_total_seconds) + " 秒" },
      { num: seconds(row.head_total_seconds) + " 秒" },
      { num: seconds(row.delta_total_seconds) + " 秒" },
    ]);
  }
  if (!cmp.queries_comparable) {
    text(el("query-compare"), cmp.queries_reason || "SQL は比較不能です");
  } else if ((cmp.queries || []).length === 0) {
    text(el("query-compare"), "比較できるSQLの差分はありません");
  } else {
    text(el("query-compare"), "SQLの差分です。合計処理時間の差（秒）で、表示中から比較元を引いています。");
    showDiff("query-diff-wrap", "query-diff-body", cmp.queries, (row) => [
      row.fingerprint || "",
      { num: seconds(row.base_total_seconds) + " 秒" },
      { num: seconds(row.head_total_seconds) + " 秒" },
      { num: seconds(row.delta_total_seconds) + " 秒" },
    ]);
  }
}

function pickAutoBase(runs, current) {
  if (!current) return "";
  for (const run of runs) {
    if (run.id === current.id) continue;
    if (run.mode !== current.mode) continue;
    if (run.benchmark_status !== "pass") continue;
    if (run.score == null || run.score === 0) continue;
    return run.id;
  }
  return "";
}

async function loadHints(id) {
  const list = el("hints");
  list.replaceChildren();
  try {
    const data = await getJSON("/api/hints?run=" + encodeURIComponent(id));
    (data.hints || []).forEach((hint) => {
      const li = document.createElement("li");
      text(li, hint.title + " " + hint.detail);
      list.appendChild(li);
    });
    if ((data.hints || []).length === 0) {
      const li = document.createElement("li");
      text(li, "いま目立つ確認ポイントはありません。");
      list.appendChild(li);
    }
  } catch (err) {
    const li = document.createElement("li");
    text(li, "確認ポイントを取得できませんでした。");
    list.appendChild(li);
  }
}

function applyModeChrome() {
  text(el("mode-label"), "モード: " + modeText(state.mode));
  el("mode-local").classList.toggle("active", state.mode === "local");
  el("mode-remote").classList.toggle("active", state.mode === "remote");
  const button = el("primary-action");
  text(button, state.mode === "local" ? "計測実行" : "ログ収集・解析");
  button.disabled = !state.executionEnabled || state.executionStatus === "running";
  el("exec-disabled").hidden = state.executionEnabled;
  el("hosts-panel").hidden = state.mode !== "remote";
}

function renderExecution(ex) {
  state.executionStatus = ex.status || "idle";
  const label = { idle: "待機中", running: "実行中", done: "完了", failed: "失敗" }[ex.status] || "待機中";
  text(el("exec-status"), label);
  text(el("exec-step"), ex.step || "");
  if (ex.status === "running" || ex.status === "done" || ex.status === "failed") {
    text(el("exec-elapsed"), "経過: " + (ex.elapsed_seconds || 0) + " 秒");
  } else {
    text(el("exec-elapsed"), "");
  }
  text(el("exec-log"), ex.log || "");
  applyModeChrome();
}

async function loadAll() {
  showError("");
  const selected = state.current && state.current.run ? state.current.run.id : "";
  const baseID = state.baseID;
  try {
    const session = await getJSON("/api/session");
    state.mode = session.mode || "remote";
    state.executionEnabled = !!session.execution_enabled;
    renderExecution(session.execution || { status: "idle", step: "" });
    const hostList = el("hosts");
    hostList.replaceChildren();
    (session.hosts || []).forEach((host) => {
      const li = document.createElement("li");
      text(li, host.name + " / " + host.address);
      hostList.appendChild(li);
    });
    if ((session.hosts || []).length === 0) {
      const li = document.createElement("li");
      text(li, "接続先は設定ファイルで指定します。鍵の中身は表示しません。");
      hostList.appendChild(li);
    }
    const queryMode = state.historyScope === "all" ? "all" : state.mode;
    const list = await getJSON("/api/runs?limit=100&mode=" + encodeURIComponent(queryMode));
    state.allRuns = list.runs || [];
    state.runs = state.historyScope === "all" ? state.allRuns : state.allRuns.filter((run) => run.mode === state.mode);
    const pool = state.runs.length ? state.runs : state.allRuns;
    if (pool.length === 0) {
      state.current = null;
      ["score"].forEach((id) => setVisible(id, false));
      el("empty").hidden = false;
      text(el("run-meta"), "このモードの計測はまだありません");
      text(el("score-value"), "未計測");
      text(el("log-range"), "");
      clearTable("endpoint-body");
      clearTable("query-body");
      clearTable("history-body");
      clearTable("trend-body");
      el("recent").replaceChildren();
      resetCompareTables();
      renderRuns();
      applyModeChrome();
      return;
    }
    const still = pool.some((run) => run.id === selected);
    const id = still ? selected : pool[0].id;
    const detail = await getJSON("/api/runs/" + encodeURIComponent(id));
    await renderDetail(detail);
    if (state.compareOff) {
      state.baseID = "";
    } else if (baseID && baseID !== id && pool.some((run) => run.id === baseID)) {
      state.baseID = baseID;
    } else if (!baseID) {
      state.baseID = pickAutoBase(pool, detail.run);
    } else {
      state.baseID = "";
    }
    renderRuns();
    await renderCompare();
    applyModeChrome();
  } catch (err) {
    showError(err.message || "画面の取得に失敗しました");
  }
}

el("refresh").addEventListener("click", () => { loadAll().catch((err) => showError(err.message)); });
el("run-select").addEventListener("change", async (ev) => {
  try {
    showError("");
    const detail = await getJSON("/api/runs/" + encodeURIComponent(ev.target.value));
    await renderDetail(detail);
    if (state.baseID === detail.run.id) state.baseID = "";
    renderRuns();
    await renderCompare();
  } catch (err) {
    showError(err.message || "計測の取得に失敗しました");
  }
});
el("base-select").addEventListener("change", async (ev) => {
  state.baseID = ev.target.value;
  state.compareOff = state.baseID === "";
  try {
    showError("");
    await renderCompare();
  } catch (err) {
    showError(err.message || "比較の取得に失敗しました");
  }
});
["api", "sql"].forEach((kind) => {
  ["sort", "order", "limit"].forEach((part) => {
    el(kind + "-" + part).addEventListener("change", () => {
      state.pages[kind] = 0;
      renderRank(kind).catch((err) => showError(err.message));
    });
  });
  el(kind + "-search").addEventListener("input", () => {
    state.pages[kind] = 0;
    renderRank(kind).catch((err) => showError(err.message));
  });
  el(kind + "-prev").addEventListener("click", () => {
    if (state.pages[kind] > 0) {
      state.pages[kind] -= 1;
      renderRank(kind).catch((err) => showError(err.message));
    }
  });
  el(kind + "-next").addEventListener("click", () => {
    const pageCount = Math.max(1, Math.ceil((state.rankTotal[kind] || 0) / pageSizeOf(kind)));
    if (state.pages[kind] + 1 < pageCount) {
      state.pages[kind] += 1;
      renderRank(kind).catch((err) => showError(err.message));
    }
  });
});
document.querySelectorAll(".tab").forEach((button) => {
  button.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((tab) => tab.classList.remove("active"));
    button.classList.add("active");
    ["api", "sql", "trend", "history"].forEach((name) => {
      el("panel-" + name).hidden = button.dataset.tab !== name;
    });
  });
});
el("history-scope").addEventListener("change", (ev) => {
  state.historyScope = ev.target.value;
  loadAll().catch((err) => showError(err.message));
});
async function switchMode(mode) {
  if (state.executionStatus === "running") {
    showError("実行中はモードを変更できません");
    return;
  }
  await postJSON("/api/mode", { mode });
  state.mode = mode;
  state.current = null;
  state.baseID = "";
  state.compareOff = false;
  await loadAll();
}
el("mode-local").addEventListener("click", () => { switchMode("local").catch((err) => showError(err.message)); });
el("mode-remote").addEventListener("click", () => { switchMode("remote").catch((err) => showError(err.message)); });
el("primary-action").addEventListener("click", async () => {
  try {
    showError("");
    const path = state.mode === "local" ? "/api/actions/local-cycle" : "/api/actions/remote-collect";
    await postJSON(path, {});
    renderExecution({ status: "running", step: "開始しました", elapsed_seconds: 0 });
  } catch (err) {
    showError(err.message);
  }
});
el("score-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  if (!state.current) return;
  const status = el("manual-status").value;
  const raw = el("manual-value").value;
  const payload = { status, note: "" };
  if (raw !== "") payload.value = Number(raw);
  try {
    await postJSON("/api/runs/" + encodeURIComponent(state.current.run.id) + "/score", payload);
    await loadAll();
  } catch (err) {
    showError(err.message);
  }
});

setInterval(() => {
  if (state.executionStatus !== "running") return;
  getJSON("/api/execution").then((ex) => {
    renderExecution(ex);
    if (ex.status !== "running") loadAll().catch(() => {});
  }).catch(() => {});
}, 2000);

loadAll().catch((err) => showError(err.message || "初期表示に失敗しました"));
