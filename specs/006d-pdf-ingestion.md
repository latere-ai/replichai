---
title: "PDF ingestion"
status: draft
---

# 006d — PDF ingestion

**Status:** draft
**Scope:** turning a dropped PDF into a paper identity, the deferred half of 001, now that a submission surface needs it. A child of [006](006-web-application.md).
**Depends on:** [001](001-paper-identity-and-slugs.md) (the `Identity` it produces; the content-digest path it completes), [006a](006a-http-api-and-streaming.md) (the multipart upload it receives).

## Problem

001 resolves a *reference* (a DOI, an arXiv id, a URL) and deliberately left the PDF path deferred: "PDF text extraction for the content-digest path digests the fetched bytes for now." The web surface makes that path real, because dragging a paper in is the most natural way to submit one, and a dropped file has no URL to resolve. So this spec extracts an identity from bytes: read the text, find the identifier the paper declares, and fall back to a content digest only when there is none.

## Decisions

**Prefer the paper's own declared identifier over the digest.** A PDF usually carries its DOI or arXiv id: on the first page, in the metadata, or in a footer. Extract the text and look for one with 001's existing patterns; a found DOI or arXiv id resolves exactly as the URL path does (arXiv still gets its metadata lookup, so preprint and published collapse). This keeps a dropped PDF and a pasted link to the same paper resolving to the *same identity*, which is the purpose of 001. Only a PDF that declares no identifier falls to `sha256` of its extracted text.

**Digest the extracted text, not the raw bytes.** 001's note digested fetched bytes as a stand-in; that makes two encodings of the same paper two identities. The real rule is the one 001 always stated, `sha256` of the *extracted text*, so re-exporting a PDF does not change the paper's identity. This spec is where that becomes true.

**Extraction is a bounded, offline step.** Text is pulled from the PDF locally, with no network and no model, so identity resolution stays deterministic and cheap, the property 001 established. A PDF that yields no extractable text (a pure scan) is a submission error the user is told about, not a silent digest of nothing.

**It plugs into `paper.Resolve` as another reference kind.** The uploaded bytes become a resolvable input alongside a URL; the classification/slug/identity machinery is 001's, unchanged. This spec adds *how to read a PDF*, not a second identity model.

## Interface (sketch)

```go
// In internal/paper (or a sibling): resolve an uploaded document to an identity.
func ResolvePDF(ctx context.Context, doc []byte, opts ...Option) (Identity, error)

// Internally: extract text, reuse extractDOI / extractArXiv, else digest the text.
func extractText(doc []byte) (string, error) // local, no network
```

The HTTP handler (006a) hands `POST /papers`' multipart body here; everything downstream (the job, the audit, publishing) sees an ordinary `Identity`.

## Acceptance criteria

1. A PDF that declares a DOI resolves to that DOI, identical to resolving the DOI as a reference: a dropped PDF and the pasted DOI produce the same identity.
2. A PDF that declares an arXiv id resolves through the same metadata lookup as the URL path (upgrading to a DOI when the preprint carries one).
3. A PDF with no declared identifier resolves to `sha256` of its *extracted text*; two different byte-level exports of the same text produce the same identity.
4. A PDF with no extractable text is a reported error, not a digest of empty input.
5. Extraction touches no network, asserted with a fetcher that fails the test if called.

## Out of scope

The upload transport and size limits (006a). Auditing the paper (the engine). OCR of scanned PDFs: a no-text PDF is an error here, not a vision problem this spec takes on. Any change to 001's identity, slug, or scheme rules; this spec only adds a reader that feeds them.

A paper that declares **no** identifier *and whose bytes will change* (a draft, a manuscript under submission) is [016](016-a-paper-that-is-not-published-yet.md), deferred. The digest rule above is correct for a fixed document and gives a new identity to every revision of a moving one, which is a model question rather than a reader question. This spec ships unchanged either way.
