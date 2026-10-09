// Package config は isucon-kit.json の読み込みと検証を行う。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const (
	// Version は現在の設定スキーマである。
	Version = 1
	// PlaceholderAddress は雛形の接続先であり、自動接続しない。
	PlaceholderAddress = "example.invalid"
	// DefaultMaxBytes は1ファイルあたりの取得上限（32MiB）である。
	DefaultMaxBytes int64 = 33554432
)

// File はユーザーが編集する設定である。
type File struct {
	Version       int            `json:"version"`
	DataDir       string         `json:"data_dir"`
	SSH           SSH            `json:"ssh"`
	Hosts         []Host         `json:"hosts"`
	Collection    Collection     `json:"collection"`
	RoutePatterns []RoutePattern `json:"route_patterns"`
	baseDir       string
}

// SSH は鍵認証の接続パラメータである。パスワードは保存しない。
type SSH struct {
	User                  string `json:"user"`
	IdentityFile          string `json:"identity_file"`
	Port                  int    `json:"port"`
	ConnectTimeoutSeconds int    `json:"connect_timeout_seconds"`
}

// Host はログと確認対象サービスを持つ競技サーバーである。
type Host struct {
	Name       string      `json:"name"`
	Address    string      `json:"address"`
	AccessLogs []AccessLog `json:"access_logs"`
	SlowLogs   []SlowLog   `json:"slow_logs"`
	Services   []string    `json:"services"`
}

// AccessLog は Nginx アクセスログのパスと形式である。
type AccessLog struct {
	Path   string `json:"path"`
	Format string `json:"format"`
}

// SlowLog は MySQL スローログのパスである。
type SlowLog struct {
	Path   string `json:"path"`
	Format string `json:"format"`
}

// Collection は取得量の上限である。
type Collection struct {
	MaxBytesPerFile int64 `json:"max_bytes_per_file"`
}

// RoutePattern はパス正規化の最初の一致を使う規則である。
type RoutePattern struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	re          *regexp.Regexp
}

// Apply はクエリ文字列を除いたパスへ規則を順に適用する。
func (f *File) ApplyRoute(path string) string {
	for _, p := range f.RoutePatterns {
		if p.re != nil && p.re.MatchString(path) {
			return p.re.ReplaceAllString(path, p.Replacement)
		}
	}
	return path
}

// BaseDir は設定ファイルのディレクトリである。
func (f *File) BaseDir() string { return f.baseDir }

// Template は接続先未設定の雛形である。実在の認証情報は含めない。
func Template() []byte {
	body := File{
		Version: Version,
		DataDir: "./.isucon-kit",
		SSH: SSH{
			User:                  "isucon",
			IdentityFile:          "",
			Port:                  22,
			ConnectTimeoutSeconds: 10,
		},
		Hosts: []Host{{
			Name:    "web1",
			Address: PlaceholderAddress,
			AccessLogs: []AccessLog{{
				Path:   "/var/log/nginx/access.log",
				Format: "json",
			}},
			SlowLogs: []SlowLog{},
			Services: []string{"nginx"},
		}},
		Collection: Collection{MaxBytesPerFile: DefaultMaxBytes},
		RoutePatterns: []RoutePattern{{
			Pattern:     `^/api/items/[0-9]+$`,
			Replacement: "/api/items/:id",
		}},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(&body)
	return buf.Bytes()
}

// WriteTemplate は新規ファイルに雛形を書く。既存ファイルは上書きしない。
func WriteTemplate(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("設定ファイル %s は既に存在します。上書きしません", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("設定ファイルの確認に失敗しました: %w", err)
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("設定ファイルのディレクトリを作成できません: %w", err)
		}
	}
	if err := os.WriteFile(path, Template(), 0o600); err != nil {
		return fmt.Errorf("設定ファイルを書き出せません: %w", err)
	}
	return nil
}

// Load は path を読み、data_dir と identity_file を設定ファイル基準の絶対パスへ解決する。
func Load(path string) (*File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("設定ファイルのパスを解決できません: %w", err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("設定ファイル %s がありません。`isucon-kit init` で雛形を作成してください", path)
		}
		return nil, fmt.Errorf("設定ファイルを読めません: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("設定ファイルの JSON が不正です: %w", err)
	}
	f.baseDir = filepath.Dir(abs)
	if f.DataDir == "" {
		return nil, errors.New("data_dir が空です")
	}
	if !filepath.IsAbs(f.DataDir) {
		f.DataDir = filepath.Join(f.baseDir, f.DataDir)
	}
	if f.SSH.IdentityFile != "" && !filepath.IsAbs(f.SSH.IdentityFile) {
		f.SSH.IdentityFile = filepath.Join(f.baseDir, f.SSH.IdentityFile)
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// Validate は構文と値域を検査し、正規表現をコンパイルする。
func (f *File) Validate() error {
	if f.Version != Version {
		return fmt.Errorf("version は %d である必要があります（現在: %d）", Version, f.Version)
	}
	if f.DataDir == "" {
		return errors.New("data_dir が空です")
	}
	if f.SSH.Port < 1 || f.SSH.Port > 65535 {
		return fmt.Errorf("ssh.port が不正です: %d", f.SSH.Port)
	}
	if f.SSH.ConnectTimeoutSeconds < 1 {
		return errors.New("ssh.connect_timeout_seconds は 1 以上である必要があります")
	}
	if f.SSH.User == "" || !userRe.MatchString(f.SSH.User) {
		return fmt.Errorf("ssh.user が不正です: %q", f.SSH.User)
	}
	if stringsContainControl(f.SSH.IdentityFile) {
		return errors.New("ssh.identity_file に改行などの制御文字は使えません")
	}
	if f.Collection.MaxBytesPerFile < 1 {
		return errors.New("collection.max_bytes_per_file は 1 以上である必要があります")
	}
	seen := map[string]struct{}{}
	for i, h := range f.Hosts {
		if h.Name == "" {
			return fmt.Errorf("hosts[%d].name が空です", i)
		}
		if _, ok := seen[h.Name]; ok {
			return fmt.Errorf("ホスト名 %q が重複しています", h.Name)
		}
		seen[h.Name] = struct{}{}
		if !hostNameRe.MatchString(h.Name) {
			return fmt.Errorf("ホスト名 %q に使えない文字があります", h.Name)
		}
		if h.Address == "" {
			return fmt.Errorf("ホスト %s の address が空です", h.Name)
		}
		if !IsPlaceholder(h.Address) && !validAddress(h.Address) {
			return fmt.Errorf("ホスト %s の address %q が不正です", h.Name, h.Address)
		}
		for j, a := range h.AccessLogs {
			if err := validateRemotePath(a.Path); err != nil {
				return fmt.Errorf("ホスト %s の access_logs[%d]: %w", h.Name, j, err)
			}
			if a.Format != "json" && a.Format != "ltsv" {
				return fmt.Errorf("ホスト %s の access_logs[%d].format は json か ltsv です", h.Name, j)
			}
		}
		for j, s := range h.SlowLogs {
			if err := validateRemotePath(s.Path); err != nil {
				return fmt.Errorf("ホスト %s の slow_logs[%d]: %w", h.Name, j, err)
			}
			if s.Format != "mysql" {
				return fmt.Errorf("ホスト %s の slow_logs[%d].format は mysql です", h.Name, j)
			}
		}
		for j, svc := range h.Services {
			if !serviceRe.MatchString(svc) {
				return fmt.Errorf("ホスト %s の services[%d] %q が不正です", h.Name, j, svc)
			}
		}
	}
	for i := range f.RoutePatterns {
		p := &f.RoutePatterns[i]
		if p.Pattern == "" {
			return fmt.Errorf("route_patterns[%d].pattern が空です", i)
		}
		re, err := regexp.Compile(p.Pattern)
		if err != nil {
			return fmt.Errorf("route_patterns[%d].pattern が不正な正規表現です: %w", i, err)
		}
		p.re = re
	}
	return nil
}

// IsPlaceholder は雛形アドレスで、SSH しない対象かを返す。
func IsPlaceholder(address string) bool {
	return address == "" || stringsEqualFold(address, PlaceholderAddress)
}

// NeedsAlp はアクセスログが1件でも設定されていれば true。
func (f *File) NeedsAlp() bool {
	for _, h := range f.Hosts {
		if len(h.AccessLogs) > 0 {
			return true
		}
	}
	return false
}

// NeedsPT はスローログが1件でも設定されていれば true。
func (f *File) NeedsPT() bool {
	for _, h := range f.Hosts {
		if len(h.SlowLogs) > 0 {
			return true
		}
	}
	return false
}

// NeedsSSH は雛形以外の接続先があれば true。
func (f *File) NeedsSSH() bool {
	for _, h := range f.Hosts {
		if !IsPlaceholder(h.Address) {
			return true
		}
	}
	return false
}

func validateRemotePath(path string) error {
	if path == "" {
		return errors.New("path が空です")
	}
	if path[0] != '/' {
		return fmt.Errorf("リモートパスは絶対パスである必要があります: %s", path)
	}
	if stringsContainControl(path) {
		return errors.New("パスに改行などの制御文字は使えません")
	}
	return nil
}

func validAddress(address string) bool {
	return hostRe.MatchString(address) || ipv6Re.MatchString(address)
}

func stringsContainControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func stringsEqualFold(a, b string) bool {
	return equalFoldASCII(a, b)
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

var (
	userRe     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)
	hostNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	hostRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	ipv6Re     = regexp.MustCompile(`^\[[0-9A-Fa-f:]+\]$`)
	serviceRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._-]*$`)
)
