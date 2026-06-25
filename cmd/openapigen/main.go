// Command openapigen writes the toilmaster3000 OpenAPI document to stdout.
//
// It exists to break a build cycle: the production binary embeds frontend/dist,
// the frontend's TypeScript types are generated from the OpenAPI spec, and the
// spec therefore cannot be produced by the production binary itself. This
// generator registers the same huma routes as the live server — via the shared
// server.Config and server.RegisterAPI, with a nil engine/rules because the
// handler closures are never invoked during spec generation — over a throwaway
// mux, then marshals the resulting document.
//
// Output is indented for reviewable diffs and goes to stdout only; diagnostics
// go to stderr, so `go run ./cmd/openapigen > openapi.json` yields a clean file.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/els0r/toilmaster3000/internal/server"
)

func main() {
	api := humago.New(http.NewServeMux(), server.Config())
	server.RegisterAPI(api, nil, nil, nil)

	doc, err := api.OpenAPI().MarshalJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "openapigen: marshal openapi:", err)
		os.Exit(1)
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, doc, "", "  "); err != nil {
		fmt.Fprintln(os.Stderr, "openapigen: indent openapi:", err)
		os.Exit(1)
	}
	pretty.WriteByte('\n')

	if _, err := os.Stdout.Write(pretty.Bytes()); err != nil {
		fmt.Fprintln(os.Stderr, "openapigen: write openapi:", err)
		os.Exit(1)
	}
}
