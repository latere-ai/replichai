// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"context"
	"fmt"

	"github.com/latere-ai/replichai/internal/instrument"
)

// Analysis runs the instrument's pipeline over cohort data and returns a
// headline statistic and whether it ran. In production it is a provider-sandbox
// run of analysis.command; in tests it is an in-Go stand-in. The sweep is
// agnostic to which, so the exploration needs no model and no credential.
type Analysis func(data []byte) (stat float64, ran bool)

// SweepPoint is one cell of a sensitivity sweep: the injected effect and what
// the analysis made of the data generated to carry it.
type SweepPoint struct {
	Effect   float64 `json:"effect"`   // the effect injected into the generated data
	Stat     float64 `json:"stat"`     // the statistic the analysis recovered
	Ran      bool    `json:"ran"`      // whether the analysis ran at all
	Unstable bool    `json:"unstable"` // the analysis failed, or its stat did not track the effect
}

// SweepReport is a distribution-and-sensitivity finding about the instrument.
// Every statement in it is about the instrument's own behaviour across ranges
// the paper never sampled, and never about the paper or about people.
type SweepReport struct {
	Points   []SweepPoint `json:"points"`
	Unstable []SweepPoint `json:"unstable"`
}

// Subject names what a sweep establishes, so it can never be read as a finding
// about the paper.
func (SweepReport) Subject() string {
	return "sensitivity of the instrument's analysis across generated distributions — a finding about the instrument, never about the paper or about people"
}

// Sweep runs the generated cohort across a range of injected effect sizes and
// applies the analysis to each, reporting where the analysis becomes unstable:
// where it fails to run, or where its recovered statistic does not increase with
// a larger injected effect, a monotonicity break that signals the pipeline is
// unreliable over that range. It is ungated and deterministic and uses no model
// or credential, because it claims nothing about a responder.
func Sweep(ctx context.Context, decl instrument.Instrument, base Generated, effects []float64,
	spec SpecifiedEffect, analyze Analysis) (SweepReport, error) {
	var rep SweepReport
	var prevStat float64
	var havePrev bool
	for _, e := range effects {
		g := base
		eff := spec
		eff.By = e
		g.Effect = &eff
		data, err := g.Collect(ctx, decl)
		if err != nil {
			return SweepReport{}, fmt.Errorf("generate at effect %.3f: %w", e, err)
		}
		stat, ran := analyze(data.CSV)
		pt := SweepPoint{Effect: e, Stat: stat, Ran: ran}
		// Unstable if the analysis did not run, or a larger injected effect did
		// not yield a larger recovered statistic.
		if !ran || (havePrev && stat < prevStat) {
			pt.Unstable = true
			rep.Unstable = append(rep.Unstable, pt)
		}
		rep.Points = append(rep.Points, pt)
		if ran {
			prevStat, havePrev = stat, true
		}
	}
	return rep, nil
}
