// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package doccheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// placeholderTag is the tag deploy/base carries. No image is ever pushed
// under it, so a target that pins nothing fails to pull rather than running
// whatever was released last.
const placeholderTag = "unreleased"

var (
	// ourImage matches a reference to an image this organization publishes,
	// with its tag and digest when present.
	ourImage = regexp.MustCompile(`ghcr\.io/latere-ai/[a-z0-9._/-]+(:[A-Za-z0-9._-]+)?(@sha256:[0-9a-f]+)?`)
	// imageLine and newTagLine are the two places a manifest names a tag.
	imageLine  = regexp.MustCompile(`(?m)^\s*(?:-\s*)?image:\s*["']?([^"'\s#]+)`)
	newTagLine = regexp.MustCompile(`(?m)^\s*newTag:\s*["']?([^"'\s#]+)`)
	// releaseTag is a version the release pipeline builds an image for.
	releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	// releaseHeading is a released section of the changelog.
	releaseHeading = regexp.MustCompile(`(?m)^## (v\d+\.\d+\.\d+) - \d{4}-\d{2}-\d{2}$`)
)

// TestNoImageReferenceFloatsOnLatest checks every image this repository
// names, in the deploy manifests and in the Go source, for a tag that names
// no particular build. :latest was the default harness image, which was never
// published, and the base Deployment's image, which ran whatever was released
// last whenever the overlay was applied. A manifest names either the
// placeholder or a release; the Go source names no image of ours by :latest
// or with no tag at all.
func TestNoImageReferenceFloatsOnLatest(t *testing.T) {
	root := repoRoot(t)
	var manifests int
	err := filepath.WalkDir(filepath.Join(root, "deploy"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || (filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml") {
			return nil
		}
		manifests++
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range imageLine.FindAllSubmatch(body, -1) {
			ref := string(m[1])
			if !strings.HasPrefix(ref, "ghcr.io/latere-ai/") {
				continue
			}
			if tag := tagOf(ref); tag != placeholderTag && !releaseTag.MatchString(tag) {
				t.Errorf("%s: image %s is tagged %q; name the %q placeholder and pin the release in the overlay", rel, ref, tag, placeholderTag)
			}
		}
		for _, m := range newTagLine.FindAllSubmatch(body, -1) {
			if tag := string(m[1]); !releaseTag.MatchString(tag) {
				t.Errorf("%s: newTag %q is not a release; pin a vX.Y.Z the release pipeline built", rel, tag)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifests == 0 {
		t.Fatal("no manifest found under deploy/; the walk is broken, not the manifests")
	}

	fset := token.NewFileSet()
	for _, path := range sourceFiles(t, root) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, m := range ourImage.FindAllStringSubmatch(value, -1) {
				if tag := strings.TrimPrefix(m[1], ":"); m[2] == "" && (tag == "" || tag == "latest") {
					t.Errorf("%s: %s names %s, which pins no build; no default may name one", fset.Position(lit.Pos()), value, m[0])
				}
			}
			return true
		})
	}
}

// TestProductionOverlayPinsTheCurrentRelease keeps deploy/prod honest about
// what production runs. The release pipeline applies the overlay and then
// sets the image to the tag it built, so an overlay left on an older tag rolls
// production back whenever it is applied by hand. `lateregate release` stamps
// the tag in the same commit that adds the changelog section, so the two
// always name the same release.
func TestProductionOverlayPinsTheCurrentRelease(t *testing.T) {
	root := repoRoot(t)
	changelog, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	newest := releaseHeading.FindSubmatch(changelog)
	if newest == nil {
		t.Fatal("CHANGELOG.md has no released section")
	}
	overlay, err := os.ReadFile(filepath.Join(root, "deploy", "prod", "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tags := newTagLine.FindAllSubmatch(overlay, -1)
	if len(tags) != 1 {
		t.Fatalf("deploy/prod/kustomization.yaml names %d newTag lines, want exactly one for the release stamp to move", len(tags))
	}
	if got, want := string(tags[0][1]), string(newest[1]); got != want {
		t.Errorf("deploy/prod pins %s, CHANGELOG.md's newest release is %s", got, want)
	}
}

// tagOf returns the tag of an image reference, or "" when it has none. A
// colon before the last slash belongs to a registry port, not a tag.
func tagOf(ref string) string {
	ref, _, _ = strings.Cut(ref, "@")
	colon := strings.LastIndex(ref, ":")
	if colon < strings.LastIndex(ref, "/") {
		return ""
	}
	return ref[colon+1:]
}
