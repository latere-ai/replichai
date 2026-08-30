// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import "sort"

// ArtifactDomains are the places an artifact hunt goes: preprint servers,
// publishers, code hosts, research data repositories, package indexes, and the
// bibliographic services that resolve an identifier to a record.
//
// The list exists because not every sandbox can offer unrestricted egress. A
// container can; a host sandbox is allow-only by construction and refuses a
// wildcard outright. So a run in one is given a bound rather than nothing, and
// the bound is recorded in the run's environment record — which is a better
// account of an artifact hunt than "it could go anywhere", even where the
// latter is available.
//
// What it costs is real and is the reason a run says which policy it had: a
// paper whose only artifact link is an author's personal page at a university
// is not reachable under this list, and the hunt records that as "not
// fetched" rather than as "not released". A reader comparing two runs can see
// which one was bounded.
func ArtifactDomains() []string {
	domains := []string{
		// Preprints and the identifier services that resolve a reference.
		"arxiv.org", "*.arxiv.org",
		"doi.org", "*.doi.org",
		"api.crossref.org", "*.crossref.org",
		"api.openalex.org", "*.openalex.org",
		"www.semanticscholar.org", "api.semanticscholar.org", "*.semanticscholar.org",
		"dblp.org", "*.dblp.org",
		"www.biorxiv.org", "*.biorxiv.org",
		"www.medrxiv.org", "*.medrxiv.org",
		"psyarxiv.com", "*.psyarxiv.com",
		"openreview.net", "*.openreview.net",
		"aclanthology.org", "*.aclanthology.org",
		"proceedings.mlr.press", "*.mlr.press",
		"papers.nips.cc", "*.nips.cc",
		"ojs.aaai.org", "*.aaai.org",

		// Publishers.
		"dl.acm.org", "*.acm.org",
		"ieeexplore.ieee.org", "*.ieee.org",
		"link.springer.com", "*.springer.com", "*.springernature.com",
		"www.sciencedirect.com", "*.sciencedirect.com", "*.elsevier.com",
		"onlinelibrary.wiley.com", "*.wiley.com",
		"www.nature.com", "*.nature.com",
		"journals.plos.org", "*.plos.org",
		"*.tandfonline.com", "*.sagepub.com", "*.cambridge.org", "*.oup.com",

		// Code hosts, which is where a released artifact usually is.
		"github.com", "*.github.com",
		"raw.githubusercontent.com", "objects.githubusercontent.com", "codeload.github.com",
		// Project pages. A real run was blocked reaching a benchmark's
		// own site at *.github.io, which is where a released artifact
		// announces itself as often as the repository is.
		"github.io", "*.github.io",
		"gitlab.io", "*.gitlab.io",
		"readthedocs.io", "*.readthedocs.io",
		"gitlab.com", "*.gitlab.com",
		"bitbucket.org", "*.bitbucket.org",
		"huggingface.co", "*.huggingface.co",

		// Research data repositories.
		"osf.io", "*.osf.io",
		"zenodo.org", "*.zenodo.org",
		"figshare.com", "*.figshare.com",
		"dataverse.harvard.edu", "*.dataverse.org",
		"archive.org", "*.archive.org",

		// Package indexes, which the bootstrap and the reproduction both need.
		"pypi.org", "*.pypi.org", "files.pythonhosted.org",
		"registry.npmjs.org", "*.npmjs.org",
		"cran.r-project.org", "*.r-project.org",
		"proxy.golang.org", "sum.golang.org",
	}
	sort.Strings(domains)
	return domains
}
