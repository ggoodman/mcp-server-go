package tests

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// waitForNotification scans an SSE stream (lines beginning with "data: ") until it
// sees a JSON-RPC notification whose "method" field matches target. It enforces a
// bounded timeout independent of the caller stream lifetime. Returns an error on
// timeout, stream error, EOF before match, or context cancellation.
func waitForNotification(parent context.Context, r io.ReadCloser, target string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return contextError(ctx, target)
		default:
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			continue
		}
		if method, _ := m["method"].(string); method == target {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan error before seeing %s: %w", target, err)
	}
	return fmt.Errorf("stream closed before seeing %s", target)
}

func contextError(ctx context.Context, target string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("timeout waiting for %s", target)
	}
	return fmt.Errorf("context canceled waiting for %s: %v", target, ctx.Err())
}
