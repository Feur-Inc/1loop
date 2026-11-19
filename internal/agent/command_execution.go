// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"1loop/internal/core"
)

const commandStreamLimit = 2 << 20

type commandStream struct {
	mu    sync.Mutex
	data  []byte
	tail  []byte
	total int
}

func (stream *commandStream) Write(p []byte) (int, error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.total += len(p)
	stream.tail = append(stream.tail, p[max(0, len(p)-8192):]...)
	if len(stream.tail) > 8192 {
		stream.tail = append([]byte(nil), stream.tail[len(stream.tail)-8192:]...)
	}
	stream.data = append(stream.data, p[:min(len(p), commandStreamLimit-len(stream.data))]...)
	return len(p), nil
}

func (stream *commandStream) summary() string {
	if stream.total > len(stream.data) {
		return string(stream.data) + fmt.Sprintf("\n…[%d bytes exceed retained prefix; final output follows]…\n", stream.total-len(stream.data)) + string(stream.tail)
	}
	return string(stream.data)
}

type commandCapture struct{ stdout, stderr commandStream }
type savedCommandOutput struct {
	id      string
	capture *commandCapture
}

func shellDirectory(ctx context.Context, machine Machine, root, requested string) (string, error) {
	if requested == "" {
		requested = "."
	}
	if machine != nil {
		dir, err := MachinePath(ctx, machine, root, requested)
		if err != nil {
			return "", err
		}
		info, err := machine.Lstat(ctx, dir)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("workdir must be an existing directory")
		}
		return dir, nil
	}
	dir, err := toolPath(ctx, root, requested)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workdir must be an existing directory")
	}
	return dir, nil
}

func commandResult(ctx context.Context, dir string, capture *commandCapture, code int, err error, maxTokens *int) string {
	status := "completed"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = "timed_out"
	case errors.Is(err, context.Canceled):
		status = "cancelled"
	case code < 0:
		status = "execution_error"
	case err != nil || code != 0:
		status = "failed"
	}
	tokens := 10000
	if maxTokens != nil {
		tokens = min(50000, max(1000, *maxTokens))
	}
	id := ""
	if tasks, _ := ctx.Value(tasksContextKey{}).(*BackgroundTasks); tasks != nil {
		id = core.Identifier()[:16]
		tasks.mu.Lock()
		if len(tasks.logs) == 8 {
			tasks.logs = append([]savedCommandOutput(nil), tasks.logs[1:]...)
		}
		tasks.logs = append(tasks.logs, savedCommandOutput{id, capture})
		tasks.mu.Unlock()
	}
	return commandText(dir, status, code, capture, tokens, id, err)
}

// commandText is a command's result as the model reads it: a header line,
// stdout, then stderr after a [stderr] line. Sizes and the id to page the
// rest come only when output was cut; plain text, as JSON would escape every
// newline and quote and add a field for each detail.
func commandText(dir, status string, code int, capture *commandCapture, tokens int, id string, err error) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "exit %d · %s · cwd %s\n", code, status, dir)
	block := func(label, full string) bool {
		kept := headAndTail(full, tokens/2)
		if kept == "" {
			return false
		}
		if label != "" {
			builder.WriteString("[" + label + "]\n")
		}
		builder.WriteString(kept)
		if !strings.HasSuffix(kept, "\n") {
			builder.WriteString("\n")
		}
		return kept != full
	}
	stdout, stderr := capture.stdout.summary(), capture.stderr.summary()
	cut := block("", stdout)
	cut = block("stderr", stderr) || cut
	retention := capture.stdout.total > len(capture.stdout.data) || capture.stderr.total > len(capture.stderr.data)
	if (cut || retention) && id != "" {
		fmt.Fprintf(&builder, "[output_id %s · stdout %d bytes, stderr %d bytes · command_output reads the rest", id, capture.stdout.total, capture.stderr.total)
		if retention {
			builder.WriteString("; the oldest output was not kept")
		}
		builder.WriteString("]\n")
	} else if retention {
		builder.WriteString("[the oldest output was not kept]\n")
	}
	if err != nil {
		builder.WriteString("[error] " + err.Error() + "\n")
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

func readCommandOutput(ctx context.Context, raw string) (string, error) {
	var args struct {
		ID, Stream    string
		Offset, Limit *int
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", err
	}
	tasks, _ := ctx.Value(tasksContextKey{}).(*BackgroundTasks)
	if tasks == nil {
		return "", fmt.Errorf("No command outputs retained in this conversation")
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	for _, saved := range tasks.logs {
		if saved.id != args.ID {
			continue
		}
		stream := &saved.capture.stdout
		switch args.Stream {
		case "stdout":
		case "stderr":
			stream = &saved.capture.stderr
		default:
			return "", fmt.Errorf("Choose stdout or stderr")
		}
		offset, limit := 0, 8192
		if args.Offset != nil {
			offset = *args.Offset
		}
		if args.Limit != nil {
			limit = *args.Limit
		}
		if offset < 0 || offset > len(stream.data) || limit < 1 || limit > 32768 || (offset < len(stream.data) && !utf8.RuneStart(stream.data[offset])) {
			return "", fmt.Errorf("Invalid output offset or limit; retained bytes: %d", len(stream.data))
		}
		end := min(len(stream.data), offset+limit)
		for end < len(stream.data) && !utf8.RuneStart(stream.data[end]) {
			end++
		}
		next := any(nil)
		if end < len(stream.data) {
			next = end
		}
		data, _ := json.Marshal(map[string]any{"id": args.ID, "stream": args.Stream, "offset": offset,
			"text": string(stream.data[offset:end]), "next_offset": next, "total_bytes": stream.total,
			"retained_bytes": len(stream.data), "truncated": stream.total > len(stream.data)})
		return string(data), nil
	}
	return "", fmt.Errorf("Command output not found in this conversation or no longer retained")
}
