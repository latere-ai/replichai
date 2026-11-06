// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package wirejson_test

import (
	"encoding/json/v2"
	"testing"

	"github.com/latere-ai/replichai/internal/wirejson"
)

func TestOutboundWritesNilAsNullAndOrdersKeys(t *testing.T) {
	type body struct {
		Tools []string          `json:"tools"`
		Meta  map[string]string `json:"meta"`
		Text  string            `json:"text"`
	}
	got, err := json.Marshal(body{Text: "a\xffb"}, wirejson.Outbound)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"tools\":null,\"meta\":null,\"text\":\"a�b\"}"; string(got) != want {
		t.Fatalf("Outbound = %s, want %s", got, want)
	}
	got, err = json.Marshal(map[string]int{"b": 1, "a": 2, "c": 3}, wirejson.Outbound)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":2,"b":1,"c":3}`; string(got) != want {
		t.Fatalf("Outbound map = %s, want %s", got, want)
	}
}

func TestForeignReadsDuplicateKeysBadBytesAndMixedCase(t *testing.T) {
	var v struct {
		Name string
		Note string `json:"note"`
	}
	in := "{\"NAME\":\"first\",\"name\":\"second\",\"Note\":\"x\xffy\"}"
	if err := json.Unmarshal([]byte(in), &v, wirejson.Foreign); err != nil {
		t.Fatal(err)
	}
	if v.Name != "second" || v.Note != "x�y" {
		t.Fatalf("Foreign decoded %+v", v)
	}
	if err := json.Unmarshal([]byte(in), &v); err == nil {
		t.Fatal("v2 defaults accepted duplicate keys")
	}
}

func TestDigestEscapesLikeTheEncoderItReplaces(t *testing.T) {
	got, err := json.Marshal(map[string]any{"z": "<a&b> ", "a": []string(nil)}, wirejson.Digest)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"a\":null,\"z\":\"\\u003ca\\u0026b\\u003e\\u2028\"}"
	if string(got) != want {
		t.Fatalf("Digest = %s, want %s", got, want)
	}
}
