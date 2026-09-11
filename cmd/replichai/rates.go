// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/platform"
	"github.com/latere-ai/replichai/internal/runtimes"
)

// runRates answers one question before a deployment spends real money: does the
// price list name the models this backend will actually run?
//
// The naive check — read the vendor's model list and eyeball it — answers the
// wrong question. The engine asks for a *preference* ("best"), and the adapter
// resolves that against whatever the backend lists today, by substring. So the
// only honest check drives that same resolution and compares its answer to the
// card. A preference resolving to an id the card does not name is not an error:
// Card.For charges the dearest rates it holds, deliberately, so a vendor's new
// model id is never free compute. But it means the session is billed at another
// model's price, and an operator should know that before the first run rather
// than after the first invoice.
//
// The card comes from the deployment when DATABASE_URL is set — that row is
// what sessions are actually priced from — and from the compiled-in seed
// otherwise, which is what a fresh deployment will start with.
func runRates(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("rates", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runtimeFlagValue := runtimeFlag(fs, defaultCLIRuntime)
	dbURL := fs.String("database-url", os.Getenv("DATABASE_URL"), "read the live rate card from this database instead of the compiled-in seed")
	asJSON := fs.Bool("json", false, "emit machine-readable output")
	if err := fs.Parse(args); err != nil {
		return errExitQuiet
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

	// The comparison itself lives in internal/credit, beside the pricing it is
	// about, so this command and the server's boot-time report cannot drift
	// into two different answers to the same question.
	report := card.Covers(ctx, runtimes.ModelResolver{Runtime: runtime})

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"source": source, "checks": report})
	}
	printRates(stdout, source, report)
	return nil
}

// rateCard reads the price list a session would actually be charged against.
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

// printRates writes the report for a person, and says plainly what an
// inexact match means rather than leaving a reader to infer it.
func printRates(w *os.File, source string, checks []credit.Coverage) {
	_, _ = fmt.Fprintf(w, "rate card: %s\n\n", source)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PREFERENCE\tRESOLVES TO\tPRICED AS\tINPUT $/MTok\tOUTPUT $/MTok")
	fallbacks := 0
	for _, c := range checks {
		if c.Err != "" {
			// Backend errors run to many lines (a credentials failure lists
			// every source it tried). One line keeps the table a table; -json
			// carries the whole thing for somebody debugging it.
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
			"Those sessions are charged the dearest rates on the card — never free, by design,\n"+
			"but not that model's own price either. Add the id above to the card in\n"+
			"Admin → Platform (or PUT /admin/settings) to bill it at its real rate.\n", fallbacks)
	}
}

// firstLine keeps a multi-line error from breaking the table.
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
