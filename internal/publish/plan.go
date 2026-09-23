// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"errors"
	"fmt"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/paper"
)

// DefaultOrg is the GitHub organization reproductions are published under.
const DefaultOrg = "open-hai"

// Plan is everything that will be published, computed before anything is
// created. Include holds worker-written paths relative to SourceDir; Publish
// reads their bytes from there.
type Plan struct {
	RepoName   string
	SourceDir  string
	Include    []string
	Quarantine []QuarantinedFile
	Manifest   Manifest
}

// QuarantinedFile is one artifact withheld from publication, and why.
type QuarantinedFile struct {
	Path   string
	Reason string
}

// ErrNoName reports a plan that cannot be named because the slug or the suffix
// is empty.
var ErrNoName = errors.New("cannot name the repository")

// PlanPublication builds the publication plan for one audit. It is pure: no
// network, no repository, no clock, and no file reads; every artifact is
// classified by its path alone. The suffix is supplied so naming is
// deterministic and testable.
func PlanPublication(result audit.Result, id paper.Identity, suffix string) (Plan, error) {
	if id.Slug == "" || suffix == "" {
		return Plan{}, fmt.Errorf("%w: slug=%q suffix=%q", ErrNoName, id.Slug, suffix)
	}
	name := repoName(id.Slug, suffix)

	plan := Plan{RepoName: name, SourceDir: result.ArtifactDir}
	for _, a := range result.Artifacts {
		// Compiled files and known secret files never enter the plan. They are
		// neither published nor listed as withheld.
		if a.IsCompiled() || isSecretName(a.Path) {
			continue
		}
		if include, reason := Classify(Provenance{Path: a.Path, Origin: originOf(a.Path)}); include {
			plan.Include = append(plan.Include, a.Path)
		} else {
			plan.Quarantine = append(plan.Quarantine, QuarantinedFile{Path: a.Path, Reason: reason})
		}
	}

	withheld := len(plan.Quarantine) > 0
	review := ReviewClear
	if withheld {
		review = ReviewExcluded
	}
	plan.Manifest = Manifest{
		Schema: SchemaVersion,
		Paper: ManifestPaper{
			Key:    id.Key,
			Scheme: id.Scheme,
			Title:  id.Title,
			Slug:   id.Slug,
		},
		Reproduction: Reproduction{
			Repo:         DefaultOrg + "/" + name,
			MethodDigest: result.MethodDigest,
			Verdict:      result.Grade(),
			// Created is set at publish time. Rate stays nil because the rate
			// mapping is still open.
		},
		Redistribution: Redistribution{ThirdPartyData: withheld, Review: review},
	}
	return plan, nil
}
