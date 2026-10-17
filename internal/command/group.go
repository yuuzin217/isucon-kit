package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
)

// MaxCaptureBytes はローカル計測で保持する標準出力・標準エラーの上限である。
// 超えた分は読み捨て、プロセスは止めない。SSH 収集はこの上限を使わない。
const MaxCaptureBytes = 256 * 1024

// RunGroup は新しいプロセスグループでコマンドを実行する。
// 期限やキャンセルではグループ全体へ SIGKILL を送り、子プロセスが残らないようにする。
func RunGroup(ctx context.Context, name string, maxCapture int, args ...string) (stdout, stderr []byte, truncated bool, err error) {
	if maxCapture <= 0 {
		maxCapture = MaxCaptureBytes
	}
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, false, err
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, false, err
	}
	type captured struct {
		buf  []byte
		more bool
	}
	outCh := make(chan captured, 1)
	errCh := make(chan captured, 1)
	go func() {
		buf, more := readLimit(outPipe, maxCapture)
		outCh <- captured{buf, more}
	}()
	go func() {
		buf, more := readLimit(errPipe, maxCapture)
		errCh <- captured{buf, more}
	}()
	waitCh := make(chan error, 1)
	go func() {
		out := <-outCh
		errOut := <-errCh
		stdout, stderr = out.buf, errOut.buf
		truncated = out.more || errOut.more
		waitCh <- cmd.Wait()
	}()
	select {
	case err = <-waitCh:
		return stdout, stderr, truncated, err
	case <-ctx.Done():
		killGroup(cmd.Process.Pid)
		err = <-waitCh
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return stdout, stderr, truncated, fmt.Errorf("%s がタイムアウトしました: %w", name, ctx.Err())
		}
		return stdout, stderr, truncated, fmt.Errorf("%s が中断されました: %w", name, ctx.Err())
	}
}

func killGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func readLimit(r io.Reader, max int) ([]byte, bool) {
	buf := make([]byte, 0, min(max, 32*1024))
	tmp := make([]byte, 32*1024)
	more := false
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			if len(buf) < max {
				room := max - len(buf)
				if n > room {
					buf = append(buf, tmp[:room]...)
					more = true
				} else {
					buf = append(buf, tmp[:n]...)
				}
			} else {
				more = true
			}
		}
		if err != nil {
			return buf, more
		}
	}
}
