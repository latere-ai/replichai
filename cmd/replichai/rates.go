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
	"github.com/latere-ai/replichai/internal/provider"
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
	providerName := fs.String("provider", provider.DefaultName, "provider to ask")
	dbURL := fs.String("database-url", os.Getenv("DATABASE_URL"), "read the live rate card from this database instead of the compiled-in seed")
	asJSON := fs.Bool("json", false, "emit machine-readable output")
	if err := fs.Parse(args); err != nil {
		return errExitQuiet
	}

	prov, err := provider.Get(*providerName)
	if err != nil {
		return fmt.Errorf("provider %q: %w", *providerName, err)
	}

	card, source, err := rateCard(ctx, *dbURL)
	if err != nil {
		return err
	}

	// The preferences the engine actually asks for. Adding one here without
	// adding it to the engine would report on a question nobody asks.
	prefs := []string{"best", "balanced", "fast"}
	report := make([]rateCheck, 0, len(prefs))
	for _, pref := range prefs {
		check := rateCheck{Preference: pref}
		model, err := prov.ResolveModel(ctx, pref)
		if err != nil {
			check.Error = err.Error()
			report = append(report, check)
			continue
		}
		check.Model = model
		rates, err := card.For(model)
		if err != nil {
			check.Error = err.Error()
			report = append(report, check)
			continue
		}
		check.PricedAs = rates.Model
		check.Exact = rates.Model == model && !isDearest(card, model)
		check.InputPerMTok = rates.Input
		check.OutputPerMTok = rates.Output
		report = append(report, check)
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"source": source, "checks": report})
	}
	printRates(stdout, source, report)
	return nil
}

// rateCheck is what one preference resolves to and how it would be billed.
type rateCheck struct {
	Preference    string `json:"preference"`
	Model         string `json:"model,omitempty"`
	PricedAs      string `json:"priced_as,omitempty"`
	Exact         bool   `json:"exact"`
	InputPerMTok  int64  `json:"input_per_mtok,omitempty"`
	OutputPerMTok int64  `json:"output_per_mtok,omitempty"`
	Error         string `json:"error,omitempty"`
}

// isDearest reports whether a model is being priced by the fail-expensive
// fallback rather than by an entry of its own. Card.For synthesises an entry
// carrying the requested name either way, so the name alone cannot tell them
// apart — the card's own list can.
func isDearest(card credit.Card, model string) bool {
	for _, r := range card.Models {
		if r.Model == model {
			return false
		}
	}
	return true
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
func printRates(w *os.File, source string, checks []rateCheck) {
	fmt.Fprintf(w, "rate card: %s\n\n", source)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PREFERENCE\tRESOLVES TO\tPRICED AS\tINPUT $/MTok\tOUTPUT $/MTok")
	fallbacks := 0
	for _, c := range checks {
		if c.Error != "" {
			// Backend errors run to many lines (a credentials failure lists
			// every source it tried). One line keeps the table a table; -json
			// carries the whole thing for somebody debugging it.
			fmt.Fprintf(tw, "%s\t-\t-\t-\t%s\n", c.Preference, firstLine(c.Error))
			continue
		}
		priced := c.PricedAs
		if !c.Exact {
			priced = "DEAREST ON CARD"
			fallbacks++
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			c.Preference, c.Model, priced, dollars(c.InputPerMTok), dollars(c.OutputPerMTok))
	}
	_ = tw.Flush()
	if fallbacks > 0 {
		fmt.Fprintf(w, "\n%d preference(s) resolve to a model the card does not name.\n"+
			"Those sessions are charged the dearest rates on the card — never free, by design,\n"+
			"but not that model's own price either. Add the id above to the card in\n"+
			"Admin → Platform (or PUT /admin/settings) to bill it at its real rate.\n", fallbacks)
	}
}

// firstLine keeps a multi-line error from breaking the table.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// dollars renders micro-USD per million tokens the way a price list is quoted.
func dollars(micro int64) string {
	return fmt.Sprintf("$%.2f", float64(micro)/float64(credit.Dollar))
}
