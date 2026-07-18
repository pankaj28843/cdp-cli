package cdp

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"
)

const defaultIOReadSize = 64 * 1024

// IOStreamResult records both the bounded read and the independent handle
// cleanup. Chrome stream handles are always closed, including when the caller
// times out or cancels the primary context.
type IOStreamResult struct {
	BytesWritten   int    `json:"bytes_written"`
	Truncated      bool   `json:"truncated"`
	EOF            bool   `json:"eof"`
	CloseAttempted bool   `json:"close_attempted"`
	Closed         bool   `json:"closed"`
	CloseError     string `json:"close_error,omitempty"`
}

// ReadIOStream copies a CDP IO stream sequentially into dst, stopping at the
// finite maxBytes bound. IO.close runs with an independent cleanup context so a
// canceled capture cannot leak the browser-side stream handle.
func ReadIOStream(ctx context.Context, client CommandClient, sessionID, handle string, maxBytes int, dst io.Writer) (result IOStreamResult, err error) {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return result, fmt.Errorf("stream handle is required")
	}
	if maxBytes <= 0 {
		return result, fmt.Errorf("max stream bytes must be positive")
	}
	if dst == nil {
		return result, fmt.Errorf("stream destination is required")
	}

	defer func() {
		result.CloseAttempted = true
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if closeErr := client.CallSession(cleanupCtx, sessionID, "IO.close", map[string]any{"handle": handle}, nil); closeErr != nil {
			result.CloseError = closeErr.Error()
			if err == nil {
				err = fmt.Errorf("close cdp IO stream: %w", closeErr)
			}
			return
		}
		result.Closed = true
	}()

	for {
		remaining := maxBytes - result.BytesWritten
		if remaining <= 0 {
			result.Truncated = true
			return result, nil
		}
		readSize := defaultIOReadSize
		if remaining < readSize {
			readSize = remaining
		}
		var response struct {
			Data          string `json:"data"`
			Base64Encoded bool   `json:"base64Encoded"`
			EOF           bool   `json:"eof"`
		}
		if callErr := client.CallSession(ctx, sessionID, "IO.read", map[string]any{"handle": handle, "size": readSize}, &response); callErr != nil {
			return result, fmt.Errorf("read cdp IO stream: %w", callErr)
		}
		chunk := []byte(response.Data)
		if response.Base64Encoded {
			decoded, decodeErr := base64.StdEncoding.DecodeString(response.Data)
			if decodeErr != nil {
				return result, fmt.Errorf("decode base64 cdp IO stream chunk: %w", decodeErr)
			}
			chunk = decoded
		}
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
			result.Truncated = true
		}
		if len(chunk) > 0 {
			written, writeErr := dst.Write(chunk)
			result.BytesWritten += written
			if writeErr != nil {
				return result, fmt.Errorf("write cdp IO stream: %w", writeErr)
			}
			if written != len(chunk) {
				return result, io.ErrShortWrite
			}
		}
		if result.Truncated {
			return result, nil
		}
		if response.EOF {
			result.EOF = true
			return result, nil
		}
		if len(chunk) == 0 {
			return result, fmt.Errorf("cdp IO.read returned an empty non-EOF chunk")
		}
	}
}
