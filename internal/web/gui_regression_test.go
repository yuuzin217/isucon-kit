//go:build browser_e2e

package web

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/store"
)

type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestGUICompareRefreshKeepsSelection(t *testing.T) {
	chrome, err := exec.LookPath("google-chrome")
	if err != nil {
		chrome, err = exec.LookPath("google-chrome-stable")
	}
	if err != nil {
		t.Fatal("google-chrome または google-chrome-stable が必要です")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node が必要です")
	}
	st := newStore(t)
	seedComparable(t, st, "run-old", "old-run", 100, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), 1.0, 1.0)
	seedFailed(t, st, "run-bad", "bad-run", time.Date(2026, 10, 9, 0, 30, 0, 0, time.UTC))
	seedComparable(t, st, "run-new", "new-run", 180, time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC), 0.25, 0.4)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: NewMux(st, nil), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	dir := t.TempDir()
	script := filepath.Join(dir, "gui.js")
	if err := os.WriteFile(script, []byte(guiScript), 0o644); err != nil {
		t.Fatal(err)
	}
	userData := filepath.Join(os.TempDir(), "isucon-kit-chrome-gui")
	_ = os.RemoveAll(userData)
	cmd := exec.Command(chrome,
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--user-data-dir="+userData,
		"--remote-debugging-port=0",
		"about:blank",
	)
	logs := &lockedBuf{}
	cmd.Stderr = logs
	cmd.Stdout = logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = os.RemoveAll(userData)
	})
	port := waitDevtools(t, logs)
	page := "http://" + ln.Addr().String() + "/"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", script, "http://127.0.0.1:"+port, page).CombinedOutput()
	if err != nil {
		t.Fatalf("GUI 回帰: %v\n%s\nchrome: %s", err, out, logs.String())
	}
}

func seedComparable(t *testing.T, st *store.Store, id, label string, score int, at time.Time, endpointTotal, queryTotal float64) {
	t.Helper()
	err := st.CreateRun(model.Run{
		ID: id, CreatedAt: at, Label: label, Score: &score,
		BenchmarkStatus: model.BenchmarkPass, CollectionStatus: model.CollectionOK,
		AnalysisStatus: model.AnalysisOK, AccessAnalysis: model.AnalysisOK, SlowAnalysis: model.AnalysisOK,
		CollectionScope: "ファイル全体（デモ）",
	}, []model.Source{{
		HostName: "local", Kind: model.KindAccess, Format: "json", Status: model.SourceOK,
		AnalysisStatus: model.AnalysisOK, CollectedAt: at, SnapshotRel: "a.log", RemotePath: "a.jsonl",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRunMode(id, model.ModeRemote); err != nil {
		t.Fatal(err)
	}
	err = st.ReplaceAnalysis(id, store.AnalysisUpdate{
		AnalysisStatus: model.AnalysisOK, AccessAnalysis: model.AnalysisOK, SlowAnalysis: model.AnalysisOK,
		Endpoints: []model.EndpointStat{{
			Method: "GET", Route: "/old", Count: 1, TotalSeconds: endpointTotal, AvgSeconds: endpointTotal, MaxSeconds: endpointTotal,
		}},
		Queries: []model.QueryStat{{
			Fingerprint: "select a", Count: 1, TotalSeconds: queryTotal, AvgSeconds: queryTotal, MaxSeconds: queryTotal,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seedFailed(t *testing.T, st *store.Store, id, label string, at time.Time) {
	t.Helper()
	err := st.CreateRun(model.Run{
		ID: id, CreatedAt: at, Label: label,
		BenchmarkStatus: model.BenchmarkUnknown, CollectionStatus: model.CollectionFailed,
		AnalysisStatus: model.AnalysisFailed, AccessAnalysis: model.AnalysisFailed, SlowAnalysis: model.AnalysisFailed,
	}, []model.Source{{
		HostName: "web1", Kind: model.KindAccess, Format: "json", Status: model.SourceFailed,
		Error: "接続していません", AnalysisStatus: "skipped", AnalysisError: "接続していません", CollectedAt: at,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRunMode(id, model.ModeRemote); err != nil {
		t.Fatal(err)
	}
}

func waitDevtools(t *testing.T, buf *lockedBuf) string {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		text := buf.String()
		const marker = "DevTools listening on ws://127.0.0.1:"
		if i := strings.Index(text, marker); i >= 0 {
			rest := text[i+len(marker):]
			port := rest
			if j := strings.IndexAny(rest, "/\n\r "); j >= 0 {
				port = rest[:j]
			}
			if port != "" {
				return port
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("DevTools ポートを取得できません: %s", buf.String())
	return ""
}

const guiScript = `
const browserHTTP = process.argv[2];
const pageURL = process.argv[3];

function sleep(ms) { return new Promise((r) => setTimeout(r, ms)); }

async function main() {
  const timer = setTimeout(() => { console.error("script timeout"); process.exit(1); }, 25000);
  const ver = await fetch(browserHTTP + "/json/version").then((r) => r.json());
  console.error("version", ver.Browser);
  const created = await fetch(browserHTTP + "/json/new?" + encodeURIComponent(pageURL), { method: "PUT" }).then(async (r) => {
    const text = await r.text();
    console.error("new", r.status, text.slice(0, 300));
    return JSON.parse(text);
  });
  if (!created.webSocketDebuggerUrl) {
    throw new Error("page target を作れません: " + JSON.stringify(created));
  }
  const ws = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener("open", () => { console.error("ws open"); resolve(); });
    ws.addEventListener("error", () => reject(new Error("page websocket")));
  });
  let seq = 0;
  const pending = new Map();
  ws.addEventListener("message", (ev) => {
    const msg = JSON.parse(ev.data);
    if (!msg.id || !pending.has(msg.id)) return;
    const item = pending.get(msg.id);
    pending.delete(msg.id);
    if (msg.error) item.reject(new Error(JSON.stringify(msg.error)));
    else item.resolve(msg.result);
  });
  function send(method, params) {
    const id = ++seq;
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      ws.send(JSON.stringify({ id, method, params: params || {} }));
    });
  }
  await send("Runtime.enable");
  await send("Page.enable");
  async function evalValue(expression) {
    const result = await send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
    if (result.exceptionDetails) {
      throw new Error(JSON.stringify(result.exceptionDetails));
    }
    return result.result ? result.result.value : undefined;
  }
  async function waitFor(expression) {
    const deadline = Date.now() + 8000;
    let last = "";
    while (Date.now() < deadline) {
      last = await evalValue(expression);
      if (last === true) return;
      await sleep(100);
    }
    const body = await evalValue("document.body ? document.body.innerText : ''");
    throw new Error("timeout: " + expression + "\\n" + body);
  }
  await waitFor("document.getElementById('score-value') && document.getElementById('score-value').textContent === '180'");
  const labels = await evalValue("document.body.innerText");
  if (!labels.includes("遅いAPI") || !labels.includes("合計処理時間") || !labels.includes("ローカル練習") || labels.includes("ランキング基準")) {
    throw new Error("日本語の表示が足りません: " + labels.slice(0, 500));
  }
  await evalValue("document.getElementById('api-search').value = 'no-such-route'; document.getElementById('api-search').dispatchEvent(new Event('input')); true");
  await waitFor("document.getElementById('endpoint-state').textContent.includes('該当する行はありません')");
  await evalValue("document.getElementById('api-limit').value = '10'; document.getElementById('api-sort').value = 'count'; document.getElementById('api-order').value = 'asc'; document.getElementById('api-search').value = '/old'; document.getElementById('api-search').dispatchEvent(new Event('input')); true");
  await waitFor("document.getElementById('endpoint-body').innerText.includes('/old') && document.getElementById('endpoint-body').innerText.includes('ミリ秒')");
  await evalValue("document.getElementById('run-select').value = 'run-old'; document.getElementById('run-select').dispatchEvent(new Event('change')); true");
  await waitFor("document.getElementById('score-value').textContent === '100'");
  await evalValue("document.getElementById('base-select').value = 'run-new'; document.getElementById('base-select').dispatchEvent(new Event('change')); true");
  await waitFor("document.getElementById('endpoint-diff-body').innerText.includes('/old') && document.getElementById('endpoint-diff-body').innerText.includes('0.750') && document.getElementById('query-diff-body').innerText.includes('select a')");
  await evalValue("document.getElementById('refresh').click(); true");
  await sleep(300);
  await waitFor("document.getElementById('score-value').textContent === '100' && document.getElementById('run-select').value === 'run-old' && document.getElementById('endpoint-diff-body').innerText.includes('/old') && document.getElementById('base-select').value === 'run-new'");
  await evalValue("document.getElementById('base-select').value = 'run-bad'; document.getElementById('base-select').dispatchEvent(new Event('change')); true");
  await waitFor("document.getElementById('endpoint-compare').textContent.includes('比較不能') && document.getElementById('query-compare').textContent.includes('比較不能') && document.getElementById('endpoint-diff-wrap').hidden === true");
  await evalValue("document.getElementById('base-select').value = ''; document.getElementById('base-select').dispatchEvent(new Event('change')); true");
  await waitFor("document.getElementById('score-delta').textContent === '-' && document.getElementById('endpoint-diff-wrap').hidden === true && document.getElementById('endpoint-compare').textContent === ''");
  await evalValue("document.getElementById('refresh').click(); true");
  await sleep(300);
  await waitFor("document.getElementById('score-value').textContent === '100' && document.getElementById('run-select').value === 'run-old' && document.getElementById('base-select').value === '' && document.getElementById('score-delta').textContent === '-' && document.getElementById('endpoint-diff-wrap').hidden === true");
  await evalValue("document.getElementById('mode-local').click(); true");
  await waitFor("document.getElementById('mode-label').textContent.includes('ローカル練習') && document.getElementById('empty').hidden === false && document.getElementById('primary-action').textContent === '計測実行'");
  await evalValue("document.getElementById('mode-remote').click(); true");
  await waitFor("document.getElementById('score-value').textContent === '180' && document.getElementById('mode-label').textContent.includes('リモート') && document.getElementById('primary-action').textContent === 'ログ収集・解析'");
  if (ver.Browser === "") {
    throw new Error("browser version empty");
  }
  clearTimeout(timer);
  console.log("gui ok");
  process.exit(0);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
`
