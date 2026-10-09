package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "isucon-kit.json")
	if err := WriteTemplate(path); err != nil {
		t.Fatal(err)
	}
	if err := WriteTemplate(path); err == nil {
		t.Fatal("既存ファイルを上書きしました")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("権限が広すぎます: %o", info.Mode().Perm())
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !IsPlaceholder(cfg.Hosts[0].Address) {
		t.Fatalf("雛形がプレースホルダではありません: %s", cfg.Hosts[0].Address)
	}
	got := cfg.ApplyRoute("/api/items/15")
	if got != "/api/items/:id" {
		t.Fatalf("正規化: %s", got)
	}
	if cfg.ApplyRoute("/api/items/15?x=1") != "/api/items/15?x=1" {
		t.Fatal("クエリ付き文字列は呼び出し側で除いてから渡す想定です")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"BEGIN OPENSSH", "password", "AKIA"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("雛形に秘密情報が含まれています: %s", secret)
		}
	}
}

func TestRejectInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "isucon-kit.json")
	if err := WriteTemplate(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"port":    strings.Replace(string(raw), `"port": 22`, `"port": 0`, 1),
		"dup":     strings.Replace(string(raw), `"name": "web1"`, `"name": "web1"`, 1) + "",
		"regex":   strings.Replace(string(raw), `^/api/items/[0-9]+$`, `(`, 1),
		"version": strings.Replace(string(raw), `"version": 1`, `"version": 2`, 1),
	}
	// 重複ホストは雛形を2ホストに書き換える。
	cases["dup"] = strings.Replace(string(raw), `"hosts": [`, `"hosts": [
    {"name":"web1","address":"example.invalid","access_logs":[],"slow_logs":[],"services":[]},`, 1)
	for name, body := range cases {
		p := filepath.Join(dir, name+".json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil {
			t.Fatalf("%s を不正と判定しませんでした", name)
		}
	}
	if _, err := Load(filepath.Join(dir, "missing.json")); err == nil || !strings.Contains(err.Error(), "init") {
		t.Fatalf("未作成時の案内がありません: %v", err)
	}
}
