.PHONY: all frontend dist-stub build run dev-api dev-web test test-go test-frontend lint lint-go lint-frontend generate check clean package install

# The Go binary embeds frontend/dist, so the frontend must be built first; the
# frontend's types are generated from the OpenAPI spec, so generate runs first.
all: build

# generate is the contract step: dump the OpenAPI spec from the Go DTOs, then
# generate the frontend's TS types from that spec. cmd/openapigen does NOT embed
# the frontend, so this works on a clean checkout before frontend/dist exists —
# which is what breaks the spec<->frontend<->binary build cycle. Run this after
# changing any wire DTO. Both outputs (openapi.json, schema.d.ts) are committed.
generate:
	go run ./cmd/openapigen > openapi.json
	cd frontend && npm install
	cd frontend && npx openapi-typescript ../openapi.json -o src/api/schema.d.ts

frontend: generate
	cd frontend && npm run build

# dist-stub satisfies the embed directive without a node toolchain: main.go
# does `//go:embed all:frontend/dist`, which only needs the directory to be
# non-empty to compile. Go-side work (test, lint) therefore does not need the
# real SPA — no npm install, no vite build. It writes the placeholder ONLY when
# the directory is absent, so a real build is never clobbered, and the shell
# carries a visible marker so a binary accidentally built on the stub is
# obvious rather than mysteriously blank. `make smoke` is the guard that the
# shipped binary embeds the real SPA.
dist-stub:
	@if [ -d frontend/dist ]; then \
		echo "frontend/dist present - leaving the existing build untouched"; \
	else \
		mkdir -p frontend/dist; \
		printf '%s\n' \
			'<!doctype html>' \
			'<html lang="en">' \
			'  <head><meta charset="utf-8" /><title>toilmaster3000 build stub</title></head>' \
			'  <body>toilmaster3000 build stub - run make build</body>' \
			'</html>' > frontend/dist/index.html; \
		echo "wrote frontend/dist/index.html (toilmaster3000 build stub)"; \
	fi

build: frontend
	go build -o toilmaster3000 .

# Production-style run: single binary serving the embedded SPA + API on :8666.
run: build
	./toilmaster3000

# Dev: run the Go API and the vite dev server (which proxies /api -> :8666).
# Run `make dev-api` and `make dev-web` in two terminals.
dev-api: frontend
	go run .

dev-web:
	cd frontend && npm run dev

# test runs BOTH halves and aggregates their exit codes rather than stopping at
# the first failure: as plain prerequisites a Go failure meant the frontend
# suite never ran, so a red run named one side and left the other unknown.
test:
	@rc=0; \
	$(MAKE) test-go || rc=1; \
	$(MAKE) test-frontend || rc=1; \
	exit $$rc

# -race everywhere: the engine runs its cycle loop on its own goroutine while
# the HTTP handlers read the snapshots, so a data race is a real failure mode
# here, not a theoretical one. The suite is small enough that it stays fast.
# No Go test reads the built SPA, so the stub is enough to satisfy the embed.
test-go: dist-stub
	go test -race ./...

test-frontend:
	cd frontend && npm test

# lint is part of the definition of done, alongside test, and mirrors test's
# split and exit-code aggregation.
lint:
	@rc=0; \
	$(MAKE) lint-go || rc=1; \
	$(MAKE) lint-frontend || rc=1; \
	exit $$rc

# golangci-lint type-checks the root package, which embeds frontend/dist, so
# something must be there before a single linter runs — but it need not be the
# real SPA. The stub keeps a Go-only lint off the node toolchain entirely.
lint-go: dist-stub
	golangci-lint run ./...

# The frontend's type check. It lives in lint, not build: vite alone does not
# typecheck, so this is the only thing that reddens on a type error.
lint-frontend:
	cd frontend && npm run lint

# check guards against drift: regenerate the committed spec + types and fail if
# they differ from what's checked in. CI runs it on every PR; run it locally
# before committing a DTO change so you find the drift first.
check: generate
	git diff --exit-code openapi.json frontend/src/api/schema.d.ts

# package builds a fresh binary and bundles it with a starter rules file and run
# instructions into a single tm3k/ directory, tarred as toilmaster3000.tar.bz2.
# The archive explodes into one top-level tm3k/ dir (no tarbomb) so it unpacks
# the same shape `make install` lays down under /tmp. The binary reads
# .config/rules.yaml relative to its cwd, so the bundle ships the example there.
package: build
	rm -rf dist/tm3k
	mkdir -p dist/tm3k/.config
	cp toilmaster3000 dist/tm3k/
	cp examples/rules.yaml dist/tm3k/.config/rules.yaml
	cp RUN.txt dist/tm3k/
	tar cjf toilmaster3000.tar.bz2 -C dist tm3k
	@echo "packaged toilmaster3000.tar.bz2"

# install unpacks the bundle into /tmp/tm3k, recreating it from scratch so each
# install is exactly the archive (the approval log in .state/ is ephemeral). It
# then prints the run instructions shipped in the bundle.
install: package
	@echo "recreating /tmp/tm3k"
	rm -rf /tmp/tm3k
	tar xjf toilmaster3000.tar.bz2 -C /tmp
	@echo
	@cat /tmp/tm3k/RUN.txt

# clean removes build artifacts only. openapi.json and frontend/src/api/schema.d.ts
# are committed contract files (regenerate with `make generate`), not artifacts.
clean:
	rm -f toilmaster3000 toilmaster3000.tar.bz2
	rm -rf frontend/dist dist
