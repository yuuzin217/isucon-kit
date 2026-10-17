package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// MaxCaptureBytes はローカル計測で保持する標準出力と標準エラーの合計上限である。
// 超えた分は読み捨て、プロセスは止めない。SSH 収集はこの上限を使わない。
const MaxCaptureBytes = 256 * 1024

// RunGroup は新しいプロセスグループでコマンドを実行する。
// 期限やキャンセルではグループ全体へ SIGKILL を送り、子プロセスが残らないようにする。
func RunGroup(ctx context.Context, name string, maxCapture int, args ...string) (stdout, stderr []byte, truncated bool, err error) {
	return RunGroupProgress(ctx, name, maxCapture, nil, args...)
}

// RunGroupProgress は RunGroup に加え、実行中の出力を progress へ渡す。
// progress が nil のときは完了まで通知しない。完了判定はプロセスの終了であり、出力の有無では成功にしない。
func RunGroupProgress(ctx context.Context, name string, maxCapture int, progress func(string), args ...string) (stdout, stderr []byte, truncated bool, err error) {
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
	capBuf := &capture{max: maxCapture}
	var drain sync.WaitGroup
	drain.Add(2)
	go func() {
		defer drain.Done()
		readInto(outPipe, capBuf, true)
	}()
	go func() {
		defer drain.Done()
		readInto(errPipe, capBuf, false)
	}()
	waitCh := make(chan error, 1)
	go func() {
		drain.Wait()
		waitCh <- cmd.Wait()
	}()
	stop := make(chan struct{})
	var ticks sync.WaitGroup
	if progress != nil {
		ticks.Add(1)
		go func() {
			defer ticks.Done()
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					progress(capBuf.text())
				}
			}
		}()
	}
	select {
	case err = <-waitCh:
	case <-ctx.Done():
		killGroup(cmd.Process.Pid)
		err = <-waitCh
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("%s がタイムアウトしました: %w", name, ctx.Err())
		} else {
			err = fmt.Errorf("%s が中断されました: %w", name, ctx.Err())
		}
	}
	close(stop)
	ticks.Wait()
	stdout, stderr, truncated = capBuf.snapshot()
	if progress != nil {
		progress(FormatOutput(stdout, stderr, truncated))
	}
	return stdout, stderr, truncated, err
}

// FormatOutput は標準出力と標準エラーを、上限を超えたときだけ注記を付けて結合する。
func FormatOutput(stdout, stderr []byte, truncated bool) string {
	log := string(stdout)
	if len(stderr) > 0 {
		if log != "" && !strings.HasSuffix(log, "\n") {
			log += "\n"
		}
		log += string(stderr)
	}
	if truncated {
		log += "\n…出力が上限を超えたため以降を捨てました…\n"
	}
	return log
}

// ProcessAlive は pid がまだ実行中かを返す。Linux のゾンビは終了済みとみなす。
// macOS には /proc が無いため、シグナル 0 で確認する。
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	return !zombie(pid)
}

func zombie(pid int) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	text := string(data)
	i := strings.LastIndex(text, ")")
	if i < 0 || i+2 >= len(text) {
		return false
	}
	return text[i+2] == 'Z'
}

func killGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

type capture struct {
	mu   sync.Mutex
	out  []byte
	err  []byte
	max  int
	used int
	more bool
}

func (c *capture) append(stdout bool, p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.used >= c.max {
		c.more = true
		return
	}
	if c.used+len(p) > c.max {
		p = p[:c.max-c.used]
		c.more = true
	}
	if stdout {
		c.out = append(c.out, p...)
	} else {
		c.err = append(c.err, p...)
	}
	c.used += len(p)
}

func (c *capture) snapshot() (out, errb []byte, more bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.out...), append([]byte(nil), c.err...), c.more
}

func (c *capture) text() string {
	out, errb, more := c.snapshot()
	return FormatOutput(out, errb, more)
}

func readInto(r io.Reader, c *capture, stdout bool) {
	tmp := make([]byte, 32*1024)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, tmp[:n])
			c.append(stdout, chunk)
		}
		if err != nil {
			return
		}
	}
}
