// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/platform"
	"github.com/latere-ai/replichai/internal/runtimes"
	"github.com/latere-ai/replichai/internal/wirejson"
)

// runRates checks, before a deployment spends money, that the rate card names
// the models this backend will run.
//
// Reading the vendor's model list by hand is not sufficient. The engine asks
// for a preference such as "best", and the adapter resolves that against
// whatever the backend lists today, by substring. The check therefore drives
// that same resolution and compares its answer to the card. A preference that
// resolves to an id the card does not name is not an error: Card.For
// deliberately charges the dearest rates it holds, so a vendor's new model id
// is never free compute. But the session is then billed at another model's
// price, and an operator should know that before the first run rather than
// after the first invoice.
//
// The card comes from the deployment when DATABASE_URL is set, because that
// row is what sessions are priced from. Otherwise it comes from the
// compiled-in seed, which is what a fresh deployment starts with.
func runRates(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flagSet("rates", stderr)
	runtimeFlagValue := runtimeFlag(fs, defaultCLIRuntime)
	dbURL := fs.String("database-url", os.Getenv("DATABASE_URL"), "read the rate card from this database instead of the compiled-in seed")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if err := noPositionals("rates", positional, stderr); err != nil {
		return err
	}

	runtimeName := resolveRuntime(*runtimeFlagValue, os.Getenv, defaultCLIRuntime)
	_, runtime, err := controllerFor(ctx, runtimeName, 0, localOptions{})
	if err != nil {
		return fmt.Errorf("runtime %q: %w", runtimeName, err)
	}

	card, source, err := rateCard(ctx, *dbURL)
	if err != nil {
		return err
	}

	// The comparison lives in internal/credit, beside the pricing it is about,
	// so this command and the server's boot-time report cannot give different
	// answers.
	report := card.Covers(ctx, runtimes.ModelResolver{Runtime: runtime})

	if *asJSON {
		return json.MarshalEncode(jsontext.NewEncoder(stdout, jsontext.WithIndent("  ")), map[string]any{"source": source, "checks": report}, wirejson.Stored)
	}
	printRates(stdout, source, report)
	return nil
}

// rateCard reads the rate card a session would be charged against.
func rateCard(ctx context.Context, dbURL string) (credit.Card, string, error) {
	if dbURL == "" {
		return credit.DefaultCard(), "compiled-in seed", nil
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return credit.Card{}, "", fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	set, err := platform.NewPGStore(pool, nil).Settings(ctx)
	if err != nil {
		return credit.Card{}, "", fmt.Errorf("read platform settings: %w", err)
	}
	return set.Rates, "this deployment's platform_settings row", nil
}

// printRates writes the report in a human-readable form and explains what an
// inexact match means.
func printRates(w *os.File, source string, checks []credit.Coverage) {
	_, _ = fmt.Fprintf(w, "rate card: %s\n\n", source)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PREFERENCE\tRESOLVES TO\tPRICED AS\tINPUT $/MTok\tOUTPUT $/MTok")
	fallbacks := 0
	for _, c := range checks {
		if c.Err != "" {
			// Backend errors can span many lines; a credentials failure lists
			// every source it tried. Only the first line is printed so the
			// table stays aligned. The -json output carries the whole error.
			_, _ = fmt.Fprintf(tw, "%s\t-\t-\t-\t%s\n", c.Preference, firstLine(c.Err))
			continue
		}
		priced := c.Rates.Model
		if !c.Named {
			priced = "DEAREST ON CARD"
			fallbacks++
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			c.Preference, c.Model, priced, dollars(c.Rates.Input), dollars(c.Rates.Output))
	}
	_ = tw.Flush()
	if fallbacks > 0 {
		_, _ = fmt.Fprintf(w, "\n%d preference(s) resolve to a model the card does not name.\n"+
			"Sessions on those models are charged the dearest rates on the card, not the model's own price.\n"+
			"Add the model id above to the card in Admin → Platform (or PUT /admin/settings)\n"+
			"to bill it at its own rate.\n", fallbacks)
	}
}

// firstLine returns the first line of s, so a multi-line error does not break
// the table.
func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before + " …"
	}
	return s
}

// dollars renders micro-USD per million tokens the way a price list is quoted.
func dollars(micro int64) string {
	return fmt.Sprintf("$%.2f", float64(micro)/float64(credit.Dollar))
}
