// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package harness

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

// fixtureLines reads a recorded stream and repeats it until it is about target
// bytes, returning the non-empty lines and the total size.
func fixtureLines(b *testing.B, path string, target int) ([][]byte, int) {
	b.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	var lines [][]byte
	total := 0
	for total < target {
		for line := range bytes.SplitSeq(raw, []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			lines = append(lines, line)
			total += len(line) + 1
		}
	}
	return lines, total
}

var streamSizes = []struct {
	name   string
	target int
}{
	{"small", 500},
	{"medium", 10 << 10},
	{"large", 200 << 10},
}

func benchDecodeStream(b *testing.B, h Harness, fixture string) {
	for _, size := range streamSizes {
		b.Run(size.name, func(b *testing.B) {
			lines, total := fixtureLines(b, fixture, size.target)
			b.Logf("payload_bytes=%d lines=%d", total, len(lines))
			b.ReportAllocs()
			b.SetBytes(int64(total))
			for b.Loop() {
				for index, line := range lines {
					if _, err := h.Decode("worker-0", index, line); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

// BenchmarkJSONClaudeCodeStreamDecode decodes a recorded Claude Code
// stream-json run line by line, as the runtime does while a stage executes.
func BenchmarkJSONClaudeCodeStreamDecode(b *testing.B) {
	benchDecodeStream(b, NewClaudeCode(), "testdata/claude-code.jsonl")
}

// BenchmarkJSONCodexStreamDecode decodes a recorded Codex exec --json run.
func BenchmarkJSONCodexStreamDecode(b *testing.B) {
	benchDecodeStream(b, NewCodex(), "testdata/codex.jsonl")
}

// BenchmarkJSONClaudeCodeToolResultDecode decodes one stream line whose tool
// result carries a large command output, the case that makes a single line big.
func BenchmarkJSONClaudeCodeToolResultDecode(b *testing.B) {
	for _, size := range streamSizes {
		b.Run(size.name, func(b *testing.B) {
			output := strings.Repeat("epoch 12 loss 0.0412 acc 0.8317 lr 3.0e-4 grad_norm 1.82\\n", size.target/60+1)
			line := []byte(fmt.Sprintf(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"%s"}]}]},"session_id":"abc"}`, output))
			h := NewClaudeCode()
			b.Logf("payload_bytes=%d", len(line))
			b.ReportAllocs()
			b.SetBytes(int64(len(line)))
			for b.Loop() {
				events, err := h.Decode("worker-0", 3, line)
				if err != nil {
					b.Fatal(err)
				}
				if len(events) == 0 {
					b.Fatal("no events decoded")
				}
			}
		})
	}
}
