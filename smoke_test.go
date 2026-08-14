//go:build smoke

// Smoke coverage over the artifact the binary actually ships: the embedded
// frontend/dist. Nothing else in the suite reads it — the server tests inject
// an fstest.MapFS stand-in and the embed directive itself only requires a
// non-empty directory — so a vite outDir change, or an index.html that stops
// landing at the dist root, would compile, test green, and only surface at
// runtime in newSPAHandler ("read SPA shell (was frontend built?)").
//
// The build tag is load-bearing. `make dist-stub` lets the Go jobs compile
// against a placeholder, and under a plain `go test ./...` these assertions
// would run against that placeholder and pass vacuously. Tagged, they run only
// via `make smoke`, which does a real `make build` first.
package main

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// embeddedSPA is the exact view of the embedded assets that run() hands to the
// server, so the test fails wherever the binary would.
func embeddedSPA(t *testing.T) fs.FS {
	t.Helper()

	spa, err := fs.Sub(embeddedFrontend, "frontend/dist")
	require.NoError(t, err)

	return spa
}

// The shell must be the real SPA: present at the dist root, carrying the mount
// point React attaches to, and never the build stub.
func TestEmbeddedSPAShellIsTheBuiltSPA(t *testing.T) {
	shell, err := fs.ReadFile(embeddedSPA(t), "index.html")
	require.NoError(t, err, "index.html must sit at the dist root - the server reads it from there")

	require.Contains(t, string(shell), `<div id="root">`, "the shell must carry the SPA root mount")
	require.NotContains(t, string(shell), "build stub", "a build stub must never be embedded in a shipped binary")
}

// rootRelativeRef matches the bundle references vite rewrites into the shell.
// Absolute URLs (the font CDN) start with a scheme, so the leading slash keeps
// them out.
var rootRelativeRef = regexp.MustCompile(`(?:src|href)="/([^"]+)"`)

// A shell alone is not a working SPA: the hashed bundles it points at have to
// be embedded next to it. `//go:embed all:frontend/dist` takes whatever the
// directory holds, so a partial dist ships silently and 404s in the browser.
func TestEmbeddedSPAShipsTheBundlesItsShellReferences(t *testing.T) {
	spa := embeddedSPA(t)

	shell, err := fs.ReadFile(spa, "index.html")
	require.NoError(t, err)

	refs := rootRelativeRef.FindAllStringSubmatch(string(shell), -1)
	require.NotEmpty(t, refs, "the built shell must reference at least one bundled asset")

	for _, ref := range refs {
		name := strings.TrimPrefix(ref[1], "/")

		f, err := spa.Open(name)
		require.NoErrorf(t, err, "the shell references %q, which is not embedded", name)
		require.NoError(t, f.Close())
	}
}
