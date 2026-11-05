// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package anthropic

import (
	"fmt"
	"strings"
	"testing"
)

// benchEventPayloads are session events as the managed-agents API returns them
// from its event listing, one per shape Transcript translates.
func benchEventPayloads(textBytes int) map[string][]byte {
	text := strings.Repeat("The evaluation script reports 83.17 accuracy on the held-out split. ", textBytes/68+1)
	output := strings.Repeat("epoch 12 loss 0.0412 acc 0.8317 lr 3.0e-4 grad_norm 1.82\\n", textBytes/60+1)
	return map[string][]byte{
		"message":           []byte(fmt.Sprintf(`{"type":"agent.message","id":"sevt_01HZX3K9","processed_at":"2026-10-14T09:30:02.125Z","content":[{"type":"text","text":%q}]}`, text)),
		"tool_use":          []byte(fmt.Sprintf(`{"type":"agent.tool_use","id":"sevt_01HZX3KA","processed_at":"2026-10-14T09:30:03.250Z","name":"bash","input":{"command":"python eval.py --split heldout --seed 7 --epochs 40","description":%q}}`, text[:min(len(text), 200)])),
		"tool_result":       []byte(fmt.Sprintf(`{"type":"agent.tool_result","id":"sevt_01HZX3KB","processed_at":"2026-10-14T09:30:09.500Z","is_error":false,"content":[{"type":"text","text":"%s"}]}`, output)),
		"model_request_end": []byte(`{"type":"span.model_request_end","id":"sevt_01HZX3KC","processed_at":"2026-10-14T09:30:10.000Z","model_usage":{"input_tokens":1200,"output_tokens":300,"cache_read_input_tokens":900,"cache_creation_input_tokens":400}}`),
	}
}

// BenchmarkJSONSessionEventUnmarshal is translateEvent: each event of a
// session's listing is decoded into the neutral transcript shape.
func BenchmarkJSONSessionEventUnmarshal(b *testing.B) {
	for _, size := range []struct {
		name  string
		bytes int
	}{{"small", 200}, {"medium", 10 << 10}, {"large", 200 << 10}} {
		b.Run(size.name, func(b *testing.B) {
			payloads := benchEventPayloads(size.bytes)
			total := 0
			for _, p := range payloads {
				total += len(p)
			}
			b.Logf("payload_bytes=%d events=%d", total, len(payloads))
			b.ReportAllocs()
			b.SetBytes(int64(total))
			for b.Loop() {
				for _, p := range payloads {
					if ev := translateEvent(p); ev.Kind == "" {
						b.Fatal("event kind lost")
					}
				}
			}
		})
	}
}
