GO ?= go
BIN ?= bin/tdb
PKGS ?= ./...

.PHONY: all build test race vet fmt fmt-check check demo site-preview site-publish clean

all: check build

build:
	$(GO) build -trimpath -o $(BIN) ./cmd/tdb

test:
	$(GO) test -count=1 $(PKGS)

race:
	$(GO) test -count=1 -race ./ui ./history ./complete ./driver/... ./settings ./sql ./textbuf

vet:
	$(GO) vet $(PKGS)

fmt:
	gofmt -w .

fmt-check:
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then \
		echo "gofmt needed:"; echo "$$files"; exit 1; \
	fi

check: fmt-check vet test

demo:
	cd .itest && $(GO) run ./cmd/demo -root .. -out ../.artifacts/demo

# Stage the GitHub Pages site with its assets from docs/, mirroring the published layout.
site-preview:
	rm -rf .artifacts/site
	mkdir -p .artifacts/site
	cp -R site/. .artifacts/site/
	cp -R docs/img .artifacts/site/img
	cp docs/demo.gif .artifacts/site/demo.gif
	@echo "Preview staged in .artifacts/site"
	@echo "Serve it: python -m http.server -d .artifacts/site 8000"

# Mirror the site to the public repository behind GitHub Pages. The source
# repository is private and the organization is on the Free plan, so Pages can
# only serve a public repository. Needs push access to p-v-q/tdb-site.
site-publish:
	rm -rf .artifacts/site-publish
	git clone --depth 1 git@github.com:p-v-q/tdb-site.git .artifacts/site-publish
	mkdir -p .artifacts/site-publish/img
	cp -R site/. .artifacts/site-publish/
	cp -R docs/img/*.png .artifacts/site-publish/img/
	cp docs/demo.gif .artifacts/site-publish/demo.gif
	printf '# TDB site\n\nGenerated from https://github.com/p-v-q/tdb/tree/main/site.\nDo not edit by hand; run `make site-publish` there instead.\n' > .artifacts/site-publish/README.md
	cd .artifacts/site-publish && git add -A && \
	if git diff --cached --quiet; then \
		echo "Site is already up to date."; \
	else \
		git commit -m "Publish site from p-v-q/tdb@$$(git -C ../.. rev-parse --short HEAD)" && \
		git push origin HEAD:main; \
	fi

clean:
	rm -rf bin
