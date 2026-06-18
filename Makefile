.DEFAULT_GOAL := help

BINARY_NAME  := formae-plugin-hcloud
BIN_DIR      := bin
OUT_DIR      := .out
DIST_DIR     := dist/pel

# Pkl CLI overrides. `PKL` must resolve to the Pkl CLI, version >=
# PKL_MIN_VERSION. `check-pkl` (a prerequisite of install/pkl-eval/pkg-pkl)
# enforces both at run time. Override per-invocation:
#   make pkl-eval PKL=/opt/pel/bin/pkl
PKL             ?= pkl
PKL_MIN_VERSION ?= 0.30.0

# formae CLI overrides. `FORMAE` must resolve to the formae CLI. Required
# only by pkl-eval (for the agent-driven Hcloud.PluginConfig validation
# path) and the conformance tests; override per-invocation:
#   make pkl-eval FORMAE=/opt/pel/bin/formae
FORMAE ?= formae

# Plugin metadata, read from formae-plugin.pkl so this stays in sync with the
# manifest. The formae agent's plugin discovery (see
# pkg/plugin/discovery/discovery.go::DiscoverPlugins) REQUIRES a versioned
# layout, otherwise the agent never sees the plugin and conformance/apply fail
# with "timeout waiting for plugin <NS> to register":
#   <PLUGINS_DIR>/<name>/v<semver>/<name>            (binary, named exactly <name>)
#   <PLUGINS_DIR>/<name>/v<semver>/formae-plugin.pkl (manifest)
#   <PLUGINS_DIR>/<name>/v<semver>/schema/pkl/...    (schema)
# Override the base dir per-invocation: `make install PLUGINS_DIR=/tmp/plugins`.
PLUGIN_NAME    := $(shell $(PKL) eval -x 'name' formae-plugin.pkl 2>/dev/null || echo "hcloud")
PLUGIN_VERSION := $(shell $(PKL) eval -x 'version' formae-plugin.pkl 2>/dev/null || echo "0.0.0")
PLUGINS_DIR   ?= $(HOME)/.pel/formae/plugins
INSTALL_DIR   := $(PLUGINS_DIR)/$(PLUGIN_NAME)/v$(PLUGIN_VERSION)

# Orbital toolchain overrides. `OPS` must resolve to the pel/orbital `ops`
# binary — NOT the Nanos/microvm `ops` shipped at ~/.ops/bin/ops (which also
# answers to bare `ops` on PATH). `check-ops` enforces this at build time.
# Override per-invocation: `make pkg OPS=/opt/pel/bin/ops CHANNEL=dev`.
OPS     ?= ops
CHANNEL ?= stable

# All schema .pkl files, found recursively. Underscore-prefixed (`_*.pkl`)
# files are dev/test scaffolding and are skipped. Shell `find` is used so
# nested modules (e.g. compute/server.pkl) are covered, not just the top level.
.PHONY: help build install test test-live-hcloud test-integration vet lint pkl-eval pkg-pkl clean tidy dist pkg publish check-ops check-pkl setup-credentials clean-environment conformance-test conformance-test-crud conformance-test-discovery conformance-cleanup

help: ## List available targets with their descriptions
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} \
	/^[a-zA-Z_-]+:.*##/ { printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

build: ## Build the plugin binary into ./bin/
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) ./pkg

install: check-pkl build ## Install the plugin into ~/.pel/formae/plugins/<name>/v<version>/ (versioned layout required by the formae agent's plugin discovery)
	@# Remove any prior install of this plugin name AND the legacy flat HETZNER/
	@# layout (which predates the versioned-layout discovery contract and silently
	@# fails to register with the agent). Clean slate per install.
	@rm -rf $(PLUGINS_DIR)/$(PLUGIN_NAME) $(PLUGINS_DIR)/HETZNER
	@mkdir -p $(INSTALL_DIR)/schema/pkl
	@cp $(BIN_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(PLUGIN_NAME)
	@cp formae-plugin.pkl $(INSTALL_DIR)/formae-plugin.pkl
	@cp -R schema/pkl/* $(INSTALL_DIR)/schema/pkl/
	@cp Hcloud.pkl $(INSTALL_DIR)/Hcloud.pkl
	@cp Hcloud.pkl $(PLUGINS_DIR)/Hcloud.pkl
	@echo "Installed $(PLUGIN_NAME) v$(PLUGIN_VERSION) -> $(INSTALL_DIR)"

# DIST_INSTALL_DIR mirrors INSTALL_DIR exactly (the versioned plugin-discovery
# layout) so an .opkg built from dist/pel produces a tree the formae agent's
# discovery can resolve — same paths as `make install`, just under dist/pel.
# The legacy unversioned plugins/HETZNER/ layout was silently undiscoverable
# after install because the agent walks <PLUGINS_DIR>/<name>/v<semver>/.
DIST_INSTALL_DIR := $(DIST_DIR)/plugins/$(PLUGIN_NAME)/v$(PLUGIN_VERSION)

dist: check-pkl build ## Stage the installable tree into dist/pel/ (mirrors `install`'s versioned discovery layout) for packaging
	@rm -rf dist
	@mkdir -p $(DIST_INSTALL_DIR)/schema/pkl
	@# Versioned plugin layout (matches INSTALL_DIR / the discovery contract):
	@cp $(BIN_DIR)/$(BINARY_NAME) $(DIST_INSTALL_DIR)/$(PLUGIN_NAME)
	@cp formae-plugin.pkl $(DIST_INSTALL_DIR)/formae-plugin.pkl
	@cp -R schema/pkl/* $(DIST_INSTALL_DIR)/schema/pkl/
	@cp Hcloud.pkl $(DIST_INSTALL_DIR)/Hcloud.pkl
	@# Root-level resolver file consumed by the plugins:/Hcloud.pkl lookup:
	@cp Hcloud.pkl $(DIST_DIR)/plugins/Hcloud.pkl
	@echo "Staged installable tree -> $(DIST_INSTALL_DIR)"

check-ops: ## Verify $(OPS) is the pel/orbital ops (NOT the Nanos/microvm ops at ~/.ops/bin/ops)
	@command -v $(OPS) >/dev/null 2>&1 || { \
		echo "ERROR: '$(OPS)' not found on PATH."; \
		echo "The pel/orbital 'ops' toolchain is required for this target."; \
		echo "Install it:"; \
		echo '  bash -c "$$(curl -fsSL https://hub.platform.engineering/get/setup.sh)" -- install --yes orbital'; \
		echo "Then either put /opt/pel/bin on PATH ahead of ~/.ops/bin, or override directly:"; \
		echo "  make pkg OPS=/opt/pel/bin/ops"; \
		exit 1; \
	}
	@out=$$($(OPS) opkg build --help 2>&1); \
	if echo "$$out" | grep -qi 'unknown command'; then \
		echo "ERROR: '$(OPS)' is the Nanos/microvm ops, NOT the pel/orbital ops."; \
		echo "Resolved to: $$(command -v $(OPS))"; \
		echo ""; \
		echo "Two different 'ops' binaries share this name:"; \
		echo "  1. Nanos/microvm ops (~/.ops/bin/ops) — subcommands: build/run/image/instance/volume/pkg."; \
		echo "  2. pel/orbital ops — what formae uses: 'ops opkg build' and 'ops publish --repo pel'."; \
		echo ""; \
		echo "The probe '$(OPS) opkg build --help' failed with 'unknown command', which only the"; \
		echo "Nanos tool produces. The pel/orbital ops understands 'opkg' as a subcommand."; \
		echo ""; \
		echo "Fix: install the pel/orbital toolchain and ensure /opt/pel/bin precedes ~/.ops/bin on PATH:"; \
		echo '  bash -c "$$(curl -fsSL https://hub.platform.engineering/get/setup.sh)" -- install --yes orbital'; \
		echo "or override directly:"; \
		echo "  make pkg OPS=/opt/pel/bin/ops"; \
		exit 1; \
	fi

pkg: check-ops dist ## Build the .opkg orbital package into bin/ (requires the pel 'ops' toolchain)
	$(OPS) opkg build --secure --target-path $(DIST_DIR) --output-path $(BIN_DIR)

publish: pkg ## Publish the .opkg to the registry (requires the pel 'ops' toolchain + registry credentials)
	$(OPS) publish --repo pel --channel $(CHANNEL) $(BIN_DIR)/*.opkg

test: ## Run go tests (mock-only; never hits the real hcloud API)
	go test ./...

# test-live-hcloud runs the DIRECT LIVE HCLOUD API SMOKE TESTS in
# pkg/*_integration_test.go (build tag `integration`). These call the hcloud
# Go SDK directly — they do NOT go through the formae agent or the plugin
# binary, and are NOT formae conformance tests (those live under the
# `conformance` build tag and are run via `make conformance-test-*`).
#
# This target loads .env (if present) AND sets HCLOUD_INTEGRATION=1 itself, so
# the user does NOT need to set either env var manually. It refuses to run
# without a non-empty HCLOUD_TOKEN — fail loud rather than let TestMain's silent
# exit-0 skip masquerade as a pass. The suite runs a pre- and post-clean sweep
# of resources labelled managed_by=formae-integration-test so it is safe to
# re-run. TestMain STILL keeps its own double opt-in gate (HCLOUD_INTEGRATION=1
# AND non-empty HCLOUD_TOKEN, else exit 0) so a developer invoking `go test`
# directly understands the safety design; this Makefile target just removes the
# need to set either var by hand. NEVER set HCLOUD_TOKEN to a production project
# token — populate .env from .env-sample instead.
test-live-hcloud: ## Run live hcloud API smoke tests directly against the real API (loads .env; NEVER use a prod token)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	if [ -z "$$HCLOUD_TOKEN" ]; then \
		echo "ERROR: HCLOUD_TOKEN is not set."; \
		echo "Populate .env from .env-sample (or export HCLOUD_TOKEN manually) before running this target."; \
		echo "NEVER use a production project token."; \
		exit 1; \
	fi
	if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	HCLOUD_INTEGRATION=1 go test -tags=integration -run '^TestIntegration_' -count=1 -timeout=20m -v ./pkg/

# test-integration is a backwards-compatible alias for test-live-hcloud.
# Prefer `make test-live-hcloud` (the name makes it explicit that these are
# direct live hcloud API smoke tests, not formae conformance tests).
test-integration: test-live-hcloud ## Alias for test-live-hcloud (live hcloud API smoke tests)

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint (fails if the linter is not installed; install via `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`)
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found on PATH."; \
		echo "Install it with:"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
		exit 1; \
	}
	golangci-lint run ./...

check-pkl: ## Verify $(PKL) is installed and reports version >= $(PKL_MIN_VERSION)
	@command -v $(PKL) >/dev/null 2>&1 || { \
		echo "ERROR: '$(PKL)' not found on PATH."; \
		echo "The Pkl CLI (>= $(PKL_MIN_VERSION)) is required for this target."; \
		echo "Install it from https://pkl-lang.org/main/current/pkl-cli/index.html"; \
		echo "or override the binary: make $(MAKECMDGOALS) PKL=/path/to/pkl"; \
		exit 1; \
	}
	@cur=$$($(PKL) --version 2>/dev/null | awk '{ for (i=1;i<=NF;i++) if ($$i ~ /^[0-9]+\.[0-9]+(\.[0-9]+)*$$/) { print $$i; exit } }'); \
	if [ -z "$$cur" ]; then \
		echo "ERROR: could not parse Pkl version from '$(PKL) --version'."; \
		$(PKL) --version; \
		exit 1; \
	fi; \
	if ! awk -v cur="$$cur" -v min="$(PKL_MIN_VERSION)" 'BEGIN { n=split(cur,c,"."); m=split(min,t,"."); mx=(n>m?n:m); for (i=1;i<=mx;i++) { cv=(i<=n?c[i]+0:0); tv=(i<=m?t[i]+0:0); if (cv<tv) exit 1; if (cv>tv) exit 0; } exit 0; }'; then \
		echo "ERROR: Pkl $$cur is older than the required $(PKL_MIN_VERSION)+."; \
		echo "Upgrade: https://pkl-lang.org/main/current/pkl-cli/index.html"; \
		echo "Or override: make $(MAKECMDGOALS) PKL=/path/to/pkl"; \
		exit 1; \
	fi; \
	echo "Pkl $$cur (>= $(PKL_MIN_VERSION)) OK"

pkl-eval: check-pkl pkl-eval-agent-config ## Validate Pkl files evaluate (formae-plugin.pkl, all schema/pkl/**/*.pkl, and the agent-driven Hcloud.PluginConfig path)
	$(PKL) eval -f json formae-plugin.pkl >/dev/null
	@# Hcloud.pkl imports the agent-only `formae:/Config.pkl` resolver, so it
	@# cannot be evaled standalone by the pkl CLI; pkl-eval-agent-config
	@# (above) drives the formae agent to validate it end-to-end.
	@for f in $$(find schema/pkl -type f -name '*.pkl' ! -name '_*'); do \
		echo "$(PKL) eval -f json --project-dir schema/pkl $$f"; \
		$(PKL) eval -f json --project-dir schema/pkl "$$f" >/dev/null || exit 1; \
	done

# pkl-eval-agent-config drives the formae agent to validate Hcloud.pkl
# through its real consumer: a generated config that does
# `import "plugins:/Hcloud.pkl" as Hcloud` and instantiates
# `new Hcloud.PluginConfig {}` inside `agent.resourcePlugins`. The formae:
# and plugins: module schemes are resolvable ONLY by the formae agent, so
# the pkl CLI alone cannot catch regressions in Hcloud.pkl's class shape
# (a previous conformance config failure came from exactly this gap).
#
# Robustness (the check asserts a POSITIVE readiness signal, not just the
# absence of "Pkl Error"): the agent is started against a temp plugin dir
# (so the user's real ~/.pel/formae/plugins is never mutated by a validation
# target) and the recipe polls the agent log for the HETZNER plugin
# registration line — the same handshake that proves Hcloud.PluginConfig
# parsed AND the plugin's schema advertised its resources. An agent that
# exits before registering (wrong subcommand, bad flag, bind failure, early
# crash, or a PKL parse error that does not happen to print "Pkl Error")
# fails the check loudly with the full log. Skipped (with a warning) when
# $(FORMAE) is not on PATH so `make pkl-eval` still works in environments
# without the formae toolchain.
.PHONY: pkl-eval-agent-config
pkl-eval-agent-config: check-pkl build
	@if ! command -v $(FORMAE) >/dev/null 2>&1; then \
		echo "Warning: '$(FORMAE)' not found on PATH; skipping agent-driven Hcloud.PluginConfig validation."; \
		echo "Install formae and re-run to exercise the full Hcloud.pkl integration path."; \
	else \
		tmpdir=$$(mktemp -d); \
		plugins_dir=$$tmpdir/plugins; \
		config=$$tmpdir/validate-hcloud-config.pkl; \
		$(MAKE) --no-print-directory install PLUGINS_DIR=$$plugins_dir >/dev/null; \
		printf '%s\n' \
			'amends "formae:/Config.pkl"' \
			'import "plugins:/Hcloud.pkl" as Hcloud' \
			'pluginDir = "'$$plugins_dir'"' \
			'agent {' \
			'    server { port = 0; secret = "pkl-eval-agent-config"; nodename = "formae-pkl-eval-hcloud" }' \
			'    datastore { sqlite { filePath = "'$$tmpdir'/validate.sqlite" } }' \
			'    synchronization { enabled = false }' \
			'    discovery { enabled = false }' \
			'    logging { consoleLogLevel = "info" }' \
			'    resourcePlugins {' \
			'        new Hcloud.PluginConfig {}' \
			'    }' \
			'}' \
			'cli { api { port = 0 }; disableUsageReporting = true }' \
			> $$config; \
		echo "Validating Hcloud.PluginConfig via $(FORMAE) agent start (temp plugin dir: $$plugins_dir) ..."; \
		$(FORMAE) agent start --config $$config > $$tmpdir/agent.log 2>&1 & \
		agent_pid=$$!; \
		ready=0; \
		for i in 1 2 3 4 5 6 7 8 9 10 11 12; do \
			if ! kill -0 $$agent_pid 2>/dev/null; then \
				break; \
			fi; \
			if grep -qE 'Plugin registered.*namespace=HETZNER|Spawned plugin namespace=HETZNER' $$tmpdir/agent.log 2>/dev/null; then \
				ready=1; \
				break; \
			fi; \
			sleep 0.5; \
		done; \
		kill -TERM $$agent_pid 2>/dev/null || true; \
		wait $$agent_pid 2>/dev/null || true; \
		if grep -q 'Pkl Error' $$tmpdir/agent.log; then \
			echo "ERROR: Hcloud.PluginConfig failed to evaluate under the formae agent (Pkl Error in log):"; \
			cat $$tmpdir/agent.log; \
			rm -rf $$tmpdir; \
			exit 1; \
		fi; \
		if [ $$ready -ne 1 ]; then \
			echo "ERROR: HETZNER plugin did not register under the formae agent within the readiness window."; \
			echo "       This catches wrong subcommands, bad flags, early process exit, bind/config failures,"; \
			echo "       and PKL parse errors that do not print 'Pkl Error'. Full agent log:"; \
			cat $$tmpdir/agent.log; \
			rm -rf $$tmpdir; \
			exit 1; \
		fi; \
		echo "Hcloud.PluginConfig loaded cleanly: HETZNER plugin registered under the formae agent."; \
		rm -rf $$tmpdir; \
	fi

pkg-pkl: check-pkl ## Package the Pkl schema into a .zip via `pkl project package` (output goes to .out/)
	$(PKL) project package schema/pkl --skip-publish-check

clean: ## Remove ./bin/, .out/, and dist/
	rm -rf $(BIN_DIR) $(OUT_DIR) dist

tidy: ## Run `go mod tidy`
	go mod tidy

# ---------------------------------------------------------------------------
# Conformance tests (formae conformance harness)
# ---------------------------------------------------------------------------
#
# These targets exercise the real plugin binary end-to-end via the official
# formae conformance SDK: `formae apply` / `inventory` / `extract` / `sync` /
# `destroy`. The harness boots a real formae agent (downloaded via orbital
# unless FORMAE_BINARY is set), discovers the plugin binary from
# ~/.pel/formae/plugins, and walks testdata/*.pkl for fixtures.
#
# The plugin MUST be `make install`'d before the tests run, and HCLOUD_TOKEN
# must be set (setup-credentials enforces the latter). Use the TEST filter
# to narrow the run to a single resource type, e.g.:
#
#   make conformance-test-crud TEST=ssh-key
#
# The credential check and the labelled-resource sweep are implemented inline
# below (no shell scripts are shipped with this repo). See docs/testing.md for
# the full cleanup contract and label conventions.

## setup-credentials: Verify HCLOUD_TOKEN is non-empty before running conformance tests
setup-credentials: ## Verify HCLOUD_TOKEN is set (loads .env; called by conformance targets)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	if [ -z "$$HCLOUD_TOKEN" ]; then \
		echo "ERROR: HCLOUD_TOKEN is not set."; \
		echo "Populate .env from .env-sample (or export HCLOUD_TOKEN manually) before running conformance tests."; \
		echo "NEVER use a production project token."; \
		exit 1; \
	fi

## clean-environment: Sweep hcloud resources labelled owner=formae-conformance-test
## Called before and after conformance runs. Idempotent and best-effort.
## Requires the hcloud CLI on PATH (skips with a warning if absent).
clean-environment: ## Sweep hcloud resources labelled owner=formae-conformance-test (best-effort; needs hcloud CLI)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	if [ -z "$$HCLOUD_TOKEN" ]; then \
		echo "Warning: HCLOUD_TOKEN not set. Skipping cleanup."; \
	elif ! command -v hcloud >/dev/null 2>&1; then \
		echo "Warning: hcloud CLI not found on PATH. Skipping cleanup."; \
		echo "Install with: go install github.com/hetznercloud/cli/cmd/hcloud@latest"; \
	else \
		echo "=== Sweeping hcloud resources labelled owner=formae-conformance-test ==="; \
		for sub in server load-balancer firewall volume floating-ip primary-ip certificate placement-group network ssh-key image; do \
			ids=$$(hcloud $$sub list -l owner=formae-conformance-test -o columns=id 2>/dev/null | tail -n +2 || true); \
			[ -z "$$ids" ] && { echo "  No $$sub found"; continue; }; \
			for id in $$ids; do \
				[ -z "$$id" ] && continue; \
				echo "  Deleting $$sub: $$id"; \
				hcloud $$sub delete "$$id" 2>/dev/null \
					|| echo "  Warning: failed to delete $$sub $$id"; \
			done; \
		done; \
		echo "=== Environment swept ==="; \
	fi

## conformance-cleanup: Alias for clean-environment (sweep labelled test resources)
conformance-cleanup: clean-environment ## Alias for clean-environment

## conformance-test: Run all conformance tests (CRUD + discovery)
## Usage: make conformance-test [TEST=ssh-key] [TIMEOUT=30m] [OOB_TIMEOUT=10] [OOB_DELETE_TIMEOUT=5]
conformance-test: conformance-test-crud conformance-test-discovery ## Run all conformance tests (CRUD + discovery)

## conformance-test-crud: Run only CRUD lifecycle tests
## Usage: make conformance-test-crud [TEST=ssh-key] [TIMEOUT=30m] [OOB_TIMEOUT=10] [OOB_DELETE_TIMEOUT=5]
conformance-test-crud: install setup-credentials ## Run CRUD lifecycle conformance tests (single-resource filter via TEST=...)
	@echo "Running CRUD conformance tests..."
	@FORMAE_TEST_FILTER="$(TEST)" FORMAE_TEST_TYPE=crud $(CONF_ENV) \
		$(GO) test -tags=conformance -run '^TestPluginConformance$$' -v -timeout $(TEST_TIMEOUT) ./

## conformance-test-discovery: Run only discovery tests
## Usage: make conformance-test-discovery [TEST=ssh-key] [TIMEOUT=30m]
conformance-test-discovery: install setup-credentials ## Run discovery conformance tests (single-resource filter via TEST=...)
	@echo "Running discovery conformance tests..."
	@FORMAE_TEST_FILTER="$(TEST)" FORMAE_TEST_TYPE=discovery $(CONF_ENV) \
		$(GO) test -tags=conformance -run '^TestPluginDiscovery$$' -v -timeout $(TEST_TIMEOUT) ./

# Filter for selecting a single resource type (or comma-separated list).
TEST ?=

# go test binary (re-used so GO override propagates).
GO ?= go

# Normalize TIMEOUT: bare digits (legacy minutes form) get "m" appended.
# The default has to be > FORMAE_TIMEOUT + OOB_TIMEOUT + OOB_DELETE_TIMEOUT
# so a slow test doesn't get killed by the outer go-test wrapper before the
# inner per-phase budgets can play out.
TEST_TIMEOUT := $(if $(TIMEOUT),$(if $(shell echo $(TIMEOUT) | grep -E '^[0-9]+$$'),$(TIMEOUT)m,$(TIMEOUT)),30m)

# hcloud is a fast backend — these defaults are intentionally lower than the
# kube-style defaults (FORMAE_TIMEOUT=50, OOB_TIMEOUT=15, OOB_DELETE_TIMEOUT=30)
# used by e.g. the OVH plugin. Most hcloud resources provision in seconds.
#
# FORMAE_TIMEOUT: bounds the framework's per-operation wait (PollStatus /
# WaitForResourceCompletion). 10 min is generous for the slowest types
# (server/load-balancer create).
# OOB_TIMEOUT: bounds a single OOB Create/Delete plugin RPC during the
# discovery test's CreateOOB phase.
# OOB_DELETE_TIMEOUT: bounds the post-sync inventory tombstone wait. The
# plugin Delete has already returned by this point; what we wait for here is
# hcloud's GET eventually reflecting the deletion — usually seconds.
# DISCOVERY_TIMEOUT: bounds the inventory polling window during the
# discovery test's wait for an unmanaged resource to appear.
FORMAE_TIMEOUT ?= 10
OOB_TIMEOUT ?= 10
OOB_DELETE_TIMEOUT ?= 5
DISCOVERY_TIMEOUT ?= 10
CONF_ENV := FORMAE_TEST_TIMEOUT=$(FORMAE_TIMEOUT) \
	$(if $(OOB_TIMEOUT),FORMAE_TEST_OOB_TIMEOUT=$(OOB_TIMEOUT)) \
	$(if $(OOB_DELETE_TIMEOUT),FORMAE_TEST_OOB_DELETE_TIMEOUT=$(OOB_DELETE_TIMEOUT)) \
	$(if $(DISCOVERY_TIMEOUT),FORMAE_TEST_DISCOVERY_TIMEOUT=$(DISCOVERY_TIMEOUT)) \
	FORMAE_LOG_PLUGINS=debug
