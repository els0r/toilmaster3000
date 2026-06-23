# Use huma v2 (on stdlib net/http) for the HTTP API

The tool was initially specced as "plain vanilla http handlers." Once rules
became user-authored through the UI, the API needed real request validation and
a stable contract. We adopted **huma v2** on top of the Go 1.22 stdlib mux: it
generates an OpenAPI spec and validates requests declaratively from typed
structs, which is worth one framework dependency on an otherwise dependency-light
tool.

## Consequences

- huma covers only *structural* validation (required fields, types). Semantic
  guards — regex fields must compile, and a rule must constrain at least one of
  author/type/scope/description (the reject-empty-rule footgun) — stay in service
  code.
- We keep stdlib routing; huma is the handler/validation layer, not a full web
  framework, so the lock-in is modest.
