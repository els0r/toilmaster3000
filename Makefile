.PHONY: all frontend build run dev-api dev-web test test-go test-frontend generate check clean package install

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

test: test-go test-frontend

test-go:
	go test ./...

test-frontend:
	cd frontend && npm test

# check guards against drift: regenerate the committed spec + types and fail if
# they differ from what's checked in. There is no CI, so run this before
# committing a DTO change.
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
