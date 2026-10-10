"use strict";

const state = { runs: [], current: null, baseID: "" };

const el = (id) => document.getElementById(id);

function text(node, value) {
  node.textContent = value == null ? "" : String(value);
}

function showError(message) {
  const node = el("error");
  text(node, message || "");
  node.hidden = !message;
}

async function getJSON(url) {
  const res = await fetch(url, { cache: "no-store" });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(body.error || ("取得に失敗しました (" + res.status + ")"));
  }
  return body;
}

function scoreText(score) {
  return score == null ? "未計測" : String(score);
}

function seconds(n) {
  if (n == null || Number.isNaN(Number(n))) return "-";
  return Number(n).toFixed(3);
}

function setVisible(id, on) {
  el(id).hidden = !on;
}

function clearTable(id) {
  const body = el(id);
  while (body.firstChild) body.removeChild(body.firstChild);
}

function addRow(id, cells, wrapIndex) {
  const tr = document.createElement("tr");
  cells.forEach((value, i) => {
    const td = document.createElement("td");
    if (i === wrapIndex) td.className = "wrap";
    text(td, value);
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

function renderRuns() {
  const runSel = el("run-select");
  const baseSel = el("base-select");
  runSel.replaceChildren();
  baseSel.replaceChildren();
  const none = document.createElement("option");
  text(none, "比較しない");
  none.value = "";
  baseSel.appendChild(none);
  state.runs.forEach((run) => {
    const label = (run.created_at || "") + " " + (run.label || "(no label)") + " / " + scoreText(run.score);
    const a = document.createElement("option");
    a.value = run.id;
    text(a, label);
    const b = a.cloneNode(true);
    runSel.appendChild(a);
    baseSel.appendChild(b);
  });
  if (state.current) runSel.value = state.current.run.id;
  baseSel.value = state.baseID || "";
  clearTable("history-body");
  state.runs.forEach((run) => {
    addRow("history-body", [run.created_at, run.label || "-", scoreText(run.score), run.benchmark_status]);
  });
}

function renderDetail(detail) {
  state.current = detail;
  const run = detail.run;
  text(el("run-meta"), "選択中: " + run.id + " / " + run.created_at);
  text(el("scope"), "収集範囲: " + (run.collection_scope || "-") + "。ベンチマーク1回分と一致するとは限りません。");
  text(el("timing"), detail.timing_basis || "まだ解析していません。");
  text(el("sides"), "アクセスログ: " + sideLabel(run.access_analysis) + " / スローログ: " + sideLabel(run.slow_analysis) + (detail.analysis_error ? " / " + detail.analysis_error : ""));
  text(el("score-value"), scoreText(run.score));
  text(el("score-status"), run.benchmark_status || "unknown");
  text(el("score-note"), run.note ? "メモ: " + run.note : "");
  clearTable("endpoint-body");
  const endpoints = (detail.endpoints || []).slice(0, 10);
  if (run.access_analysis === "absent") {
    text(el("endpoint-state"), "未収集");
  } else if (run.access_analysis === "failed") {
    text(el("endpoint-state"), "解析失敗");
  } else if (run.access_analysis === "pending") {
    text(el("endpoint-state"), "未解析");
  } else if (endpoints.length === 0) {
    text(el("endpoint-state"), "該当エンドポイントはありません");
  } else {
    text(el("endpoint-state"), "ランキング基準: total_seconds 降順");
    endpoints.forEach((ep) => addRow("endpoint-body", [ep.Method || ep.method, ep.Route || ep.route, ep.Count ?? ep.count, seconds(ep.TotalSeconds ?? ep.total_seconds), seconds(ep.AvgSeconds ?? ep.avg_seconds)], 1));
  }
  clearTable("query-body");
  const queries = (detail.queries || []).slice(0, 10);
  if (run.slow_analysis === "absent") {
    text(el("query-state"), "未収集");
  } else if (run.slow_analysis === "failed") {
    text(el("query-state"), "解析失敗");
  } else if (run.slow_analysis === "pending") {
    text(el("query-state"), "未解析");
  } else if (queries.length === 0) {
    text(el("query-state"), "該当クエリはありません");
  } else {
    text(el("query-state"), "ランキング基準: total_seconds 降順。表示はフィンガープリントのみです。");
    queries.forEach((q) => addRow("query-body", [q.Fingerprint || q.fingerprint, q.Count ?? q.count, seconds(q.TotalSeconds ?? q.total_seconds), seconds(q.AvgSeconds ?? q.avg_seconds)], 0));
  }
  ["status", "score", "endpoints", "queries", "history"].forEach((id) => setVisible(id, true));
  el("empty").hidden = true;
}

function resetCompareTables() {
  clearTable("endpoint-diff-body");
  clearTable("query-diff-body");
  el("endpoint-diff-wrap").hidden = true;
  el("query-diff-wrap").hidden = true;
  text(el("endpoint-compare"), "");
  text(el("query-compare"), "");
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
  if (cmp.score_delta == null) {
    text(el("score-delta"), "N/A");
  } else {
    text(el("score-delta"), String(cmp.score_delta) + " / " + pct);
  }
  text(el("score-note"), "比較: base " + cmp.base_id + " → head " + cmp.head_id + (state.current.run.note ? " / メモ: " + state.current.run.note : ""));
  if (!cmp.endpoints_comparable) {
    text(el("endpoint-compare"), cmp.endpoints_reason || "エンドポイントは比較不能です");
  } else if ((cmp.endpoints || []).length === 0) {
    text(el("endpoint-compare"), "比較可能なエンドポイント差分はありません");
  } else {
    text(el("endpoint-compare"), "Endpoint 差分（表示中 − base）累積秒。上位10件。");
    showDiff("endpoint-diff-wrap", "endpoint-diff-body", cmp.endpoints, (row) => [
      row.method || "",
      row.route || "",
      seconds(row.base_total_seconds),
      seconds(row.head_total_seconds),
      seconds(row.delta_total_seconds),
    ]);
  }
  if (!cmp.queries_comparable) {
    text(el("query-compare"), cmp.queries_reason || "SQL は比較不能です");
  } else if ((cmp.queries || []).length === 0) {
    text(el("query-compare"), "比較可能な SQL 差分はありません");
  } else {
    text(el("query-compare"), "SQL 差分（表示中 − base）累積秒。上位10件。");
    showDiff("query-diff-wrap", "query-diff-body", cmp.queries, (row) => [
      row.fingerprint || "",
      seconds(row.base_total_seconds),
      seconds(row.head_total_seconds),
      seconds(row.delta_total_seconds),
    ]);
  }
}

async function loadAll() {
  showError("");
  const selected = state.current && state.current.run ? state.current.run.id : "";
  const baseID = state.baseID;
  try {
    const list = await getJSON("/api/runs?limit=30");
    state.runs = list.runs || [];
    if (state.runs.length === 0) {
      state.current = null;
      state.baseID = "";
      ["status", "score", "endpoints", "queries", "history"].forEach((id) => setVisible(id, false));
      el("empty").hidden = false;
      text(el("run-meta"), "計測なし");
      return;
    }
    const still = state.runs.some((run) => run.id === selected);
    const id = still ? selected : state.runs[0].id;
    const detail = await getJSON("/api/runs/" + encodeURIComponent(id));
    renderDetail(detail);
    if (baseID && state.runs.some((run) => run.id === baseID) && baseID !== id) {
      state.baseID = baseID;
    } else if (!state.baseID && state.runs.length > 1 && state.runs[1].id !== id) {
      state.baseID = state.runs[1].id;
    } else if (state.baseID === id) {
      state.baseID = "";
    }
    renderRuns();
    await renderCompare();
  } catch (err) {
    showError(err.message || "画面の取得に失敗しました");
  }
}

el("refresh").addEventListener("click", () => { loadAll().catch((err) => showError(err.message)); });
el("run-select").addEventListener("change", async (ev) => {
  try {
    showError("");
    const detail = await getJSON("/api/runs/" + encodeURIComponent(ev.target.value));
    renderDetail(detail);
    renderRuns();
    await renderCompare();
  } catch (err) {
    showError(err.message || "run の取得に失敗しました");
  }
});
el("base-select").addEventListener("change", async (ev) => {
  state.baseID = ev.target.value;
  try {
    showError("");
    await renderCompare();
  } catch (err) {
    showError(err.message || "比較の取得に失敗しました");
  }
});

loadAll().catch((err) => showError(err.message || "初期表示に失敗しました"));
