package cli

import "bytes"

const maxExternalProcessOutputBytes = 64 * 1024

type boundedProcessOutput struct {
	buffer     bytes.Buffer
	maxBytes   int
	truncated  bool
	onTruncate func()
}

func (output *boundedProcessOutput) Write(value []byte) (int, error) {
	originalLength := len(value)
	remaining := output.maxBytes - output.buffer.Len()
	if remaining <= 0 {
		if originalLength > 0 && !output.truncated {
			output.truncated = true
			if output.onTruncate != nil {
				output.onTruncate()
			}
		}
		return originalLength, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		if !output.truncated {
			output.truncated = true
			if output.onTruncate != nil {
				output.onTruncate()
			}
		}
	}
	_, _ = output.buffer.Write(value)
	return originalLength, nil
}

func (output *boundedProcessOutput) String() string {
	value := output.buffer.String()
	if output.truncated {
		value += "\n<truncated>"
	}
	return value
}
