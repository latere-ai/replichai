// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package draft

import (
	"fmt"
	"strings"
)

// DescriptionSystem is what the model is told a description is.
//
// It is the platform's, not the operator's: what a project description should
// say is a product decision, and the operator's decision is only which model
// writes it. It insists on the papers and forbids a verdict, because a
// description is what a project is *about* — what it found is the reproduction's
// to say, and a model that has read neither the papers nor the runs is in no
// position to say it.
const DescriptionSystem = `You write one short paragraph describing a research reproduction project.

Rules:
- Two or three sentences. No heading, no list, no markdown.
- Say what the papers are about and what reproducing them would establish.
- Use only the details given. Never invent an author, a venue, a year, or a finding.
- Never state whether anything reproduced: no run has been read.
- Write plainly, in the third person. Do not begin with "This project".`

// Paper is the little a description needs to know about a paper: what it is
// called and what its record says. It is this package's own shape rather than
// project.Paper, so the store's type stays out of the prompt builder and this
// package imports nothing but the standard library.
type Paper struct {
	Title   string
	Ref     string
	Authors []string
	Venue   string
	Year    int
}

// DescriptionPrompt is the material a description is written from: the project's
// title and its papers' records, and nothing else.
//
// Not the PDFs, not the transcripts, not the verdicts. A description says what
// the project is about, and what it is about is which papers are in it — which
// also keeps the call small enough for the price to stay where it is sold.
func DescriptionPrompt(title string, papers []Paper) string {
	var b strings.Builder
	b.WriteString("Project title: ")
	b.WriteString(strings.TrimSpace(title))
	b.WriteString("\n\n")
	if len(papers) == 0 {
		b.WriteString("This project holds no papers yet.")
		return b.String()
	}
	b.WriteString("Papers in this project:\n")
	for _, p := range papers {
		b.WriteString("- ")
		b.WriteString(firstNonEmpty(p.Title, p.Ref))
		if len(p.Authors) > 0 {
			b.WriteString(" — ")
			b.WriteString(strings.Join(p.Authors, ", "))
		}
		if p.Venue != "" {
			b.WriteString(" — ")
			b.WriteString(p.Venue)
		}
		if p.Year != 0 {
			fmt.Fprintf(&b, " (%d)", p.Year)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
