// Package command は外部コマンドをシェル文字列へ結合せずに起動する。
package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Runner はテストで差し替えられるコマンド実行である。
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
	LookPath(name string) (string, error)
}

// OSRunner は実プロセスを起動する。
type OSRunner struct{}

// Run は name を引数分離のまま実行し、終了コードが非0なら error を返す。標準出力は残る。
func (OSRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("%s がタイムアウトしました: %w", name, ctx.Err())
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("%s が中断されました: %w", name, ctx.Err())
		}
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

// LookPath は PATH から実行ファイルを探す。
func (OSRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}
