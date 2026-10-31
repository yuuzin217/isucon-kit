// Package model は実行履歴と集計結果のドメイン型を定義する。
package model

import "time"

const (
	// BenchmarkPass はベンチマーク成功を表す。
	BenchmarkPass = "pass"
	// BenchmarkFail はベンチマーク失敗を表す。スコア未計測でもよい。
	BenchmarkFail = "fail"
	// BenchmarkUnknown は未計測を表す。スコア 0 とは区別する。
	BenchmarkUnknown = "unknown"

	CollectionOK      = "ok"
	CollectionPartial = "partial"
	CollectionFailed  = "failed"

	// AnalysisPending は収集済みで未解析である。
	AnalysisPending = "pending"
	AnalysisOK      = "ok"
	AnalysisPartial = "partial"
	AnalysisFailed  = "failed"
	// AnalysisAbsent は対象ログが設定されていない。
	AnalysisAbsent = "absent"

	SourceOK     = "ok"
	SourceFailed = "failed"

	KindAccess = "access"
	KindSlow   = "slow"

	FormatJSON   = "json"
	FormatLTSV   = "ltsv"
	FormatMySQL  = "mysql"
	FormatPGStat = "pg_stat_statements"

	// ModeLegacy はモード導入前の履歴である。
	ModeLegacy = "legacy"
	// ModeLocal はローカル練習の計測である。
	ModeLocal = "local"
	// ModeRemote はリモートサーバーの収集である。
	ModeRemote = "remote"

	ExecIdle    = "idle"
	ExecRunning = "running"
	ExecDone    = "done"
	ExecFailed  = "failed"
)

// Run は1回の収集または取り込みを表す。
type Run struct {
	ID               string
	CreatedAt        time.Time
	Label            string
	GitSHA           *string
	Note             string
	Score            *int
	BenchmarkStatus  string
	CollectionStatus string
	AnalysisStatus   string
	TimingBasis      string
	CollectionScope  string
	ToolVersions     string
	AccessAnalysis   string
	SlowAnalysis     string
	AnalysisError    string
	// Mode は local / remote / legacy。空のまま保存すると legacy になる。
	Mode string
}

// Source は収集元ファイル1件を表す。生ログ本文は持たない。
type Source struct {
	ID             int64
	RunID          string
	HostName       string
	Kind           string
	Format         string
	RemotePath     string
	SnapshotRel    string
	Status         string
	Error          string
	SizeBytes      int64
	OriginalSize   int64
	Truncated      bool
	CollectedAt    time.Time
	AnalysisStatus string
	AnalysisError  string
}

// EndpointStat は正規化済みエンドポイントの集計である。時間の単位は秒。
type EndpointStat struct {
	Method       string  `json:"method"`
	Route        string  `json:"route"`
	Count        int     `json:"count"`
	TotalSeconds float64 `json:"total_seconds"`
	AvgSeconds   float64 `json:"avg_seconds"`
	MaxSeconds   float64 `json:"max_seconds"`
	// P95Seconds は単一の alp 行からのみ採用する。複数行や複数ホストの合算では nil。
	P95Seconds *float64 `json:"p95_seconds"`
}

// QueryStat は匿名化済みフィンガープリント単位の集計である。時間の単位は秒。
type QueryStat struct {
	Fingerprint  string  `json:"fingerprint"`
	Count        int     `json:"count"`
	TotalSeconds float64 `json:"total_seconds"`
	AvgSeconds   float64 `json:"avg_seconds"`
	MaxSeconds   float64 `json:"max_seconds"`
}
