// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"

	"latere.ai/x/pkg/pricelist"
)

// TestPriceListURL checks that the card follows OpenRouter's list by default,
// another list when one is named, and none when the deployment turns it off.
func TestPriceListURL(t *testing.T) {
	for value, want := range map[string]string{
		"":                         pricelist.DefaultURL,
		"off":                      "",
		" https://prices.example ": "https://prices.example",
	} {
		got := priceListURL(func(string) string { return value })
		if got != want {
			t.Errorf("REPLICHAI_PRICE_LIST_URL=%q: %q, want %q", value, got, want)
		}
	}
}
