package processgroup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxProcessIdentityOutputBytes = 4096
const processIdentityProbeTimeout = 5 * time.Second

var (
	ErrProcessStartTimeUnavailable   = errors.New("process start identity unavailable")
	ErrProcessIdentityOutputTooLarge = errors.New("process start identity output too large")
)

// IsStrongProcessStartIdentity reports whether token is one of the opaque
// identities produced by ProcessStartTime. Other non-empty values may be
// legacy wall-clock metadata and must not be treated as OS identity.
func IsStrongProcessStartIdentity(token string) bool {
	token = strings.TrimSpace(token)
	return strings.HasPrefix(token, "proc:") || strings.HasPrefix(token, "ps:")
}

// ProcessStartTime returns an opaque identity token for the process currently
// occupying pid. The token is suitable for private ownership records, not
// public diagnostics. Linux prefers the kernel's /proc start-time field and
// other Unix hosts use a bounded ps fallback.
func ProcessStartTime(ctx context.Context, pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("process start identity requires a positive pid")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if token, err := processStartTimeFromProc(pid); err == nil && strings.TrimSpace(token) != "" {
		return "proc:" + strings.TrimSpace(token), nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, processIdentityProbeTimeout)
	defer cancel()
	token, err := runProcessIdentityProbe(probeCtx, "ps", []string{"-o", "lstart=", "-p", strconv.Itoa(pid)})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		return "", fmt.Errorf("%w: %v", ErrProcessStartTimeUnavailable, err)
	}
	return "ps:" + token, nil
}

func parseProcStatStartTime(stat string) (string, error) {
	closeParen := strings.LastIndexByte(stat, ')')
	if closeParen < 0 {
		return "", fmt.Errorf("malformed process stat")
	}
	fields := strings.Fields(stat[closeParen+1:])
	// The fields after the closing command name start at field 3. Linux's
	// starttime is field 22, which is index 19 in this suffix.
	if len(fields) <= 19 || strings.TrimSpace(fields[19]) == "" {
		return "", fmt.Errorf("process stat has no start-time field")
	}
	return strings.TrimSpace(fields[19]), nil
}

func runProcessIdentityProbe(ctx context.Context, bin string, args []string) (string, error) {
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &boundedIdentityOutput{cancel: cancel}
	stderr := &boundedIdentityOutput{cancel: cancel}
	err := Run(probeCtx, bin, args, stdout, stderr)
	if stdout.overflowed() || stderr.overflowed() {
		return "", ErrProcessIdentityOutputTooLarge
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", err
	}
	value := strings.Join(strings.Fields(stdout.string()), " ")
	if value == "" {
		return "", ErrProcessStartTimeUnavailable
	}
	return value, nil
}

type boundedIdentityOutput struct {
	mu       sync.Mutex
	data     bytes.Buffer
	overflow bool
	cancel   context.CancelFunc
}

func (w *boundedIdentityOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.overflow {
		return 0, ErrProcessIdentityOutputTooLarge
	}
	remaining := maxProcessIdentityOutputBytes - w.data.Len()
	if len(p) > remaining {
		if remaining > 0 {
			_, _ = w.data.Write(p[:remaining])
		}
		w.overflow = true
		if w.cancel != nil {
			w.cancel()
		}
		return 0, ErrProcessIdentityOutputTooLarge
	}
	return w.data.Write(p)
}

func (w *boundedIdentityOutput) overflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflow
}

func (w *boundedIdentityOutput) string() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.String()
}
