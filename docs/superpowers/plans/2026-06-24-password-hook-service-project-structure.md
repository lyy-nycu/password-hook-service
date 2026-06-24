# Password Hook Service Project Structure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the initial Go project structure from the approved design document with a compiling M1 foundation.

**Architecture:** Create a stdlib-first Go service with `cmd/server` wiring, internal packages for config/app/http/middleware/migration, and reusable `pkg/problem` and `pkg/logger` libraries. Azure integrations remain behind interfaces/stubs so the repository has stable seams for later Service Bus, Graph, and Key Vault work without introducing SDK dependencies in the scaffold.

**Tech Stack:** Go 1.26, `net/http`, `log/slog`, Dockerized Go toolchain for local verification.

---

### Task 1: Core Behavior Tests

**Files:**
- Create: `go.mod`
- Create: `internal/migration/classifier_test.go`
- Create: `internal/migration/upn_test.go`
- Create: `pkg/problem/problem_test.go`
- Create: `pkg/logger/logger_test.go`

- [x] **Step 1: Write failing tests**

Create tests for:
- Numeric CN values classify as `student_id`
- Alphanumeric institutional IDs classify as `employee_id`
- Email-shaped CN values classify as `external_email`
- Internal UPNs are built from normalized CN and configured domain
- RFC 9457 problem responses serialize with `application/problem+json`
- Logger masks fields named `password`, `passwd`, and `secret`

- [x] **Step 2: Run tests to verify they fail**

Run: `docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./...`

Expected: FAIL because the tested packages and symbols do not exist yet.

### Task 2: Foundation Packages

**Files:**
- Create: `internal/migration/classifier.go`
- Create: `internal/migration/upn.go`
- Create: `internal/migration/message.go`
- Create: `pkg/problem/problem.go`
- Create: `pkg/logger/logger.go`

- [x] **Step 1: Implement migration identity primitives**

Create `ClassifyCN`, `BuildUPN`, and `PasswordSyncMessage` with no external dependencies.

- [x] **Step 2: Implement reusable problem responses**

Create an RFC 9457 `Problem` type and `Write` helper.

- [x] **Step 3: Implement masking logger helper**

Create a `MaskAttrs` helper that replaces sensitive slog attributes with `"****"`.

- [x] **Step 4: Run focused tests**

Run: `docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./internal/migration ./pkg/problem ./pkg/logger`

Expected: PASS.

### Task 3: HTTP/App Skeleton

**Files:**
- Create: `cmd/server/main.go`
- Create: `internal/app/app.go`
- Create: `internal/httpserver/server.go`
- Create: `internal/handler/hook.go`
- Create: `internal/middleware/hmac.go`
- Create: `internal/middleware/ratelimit.go`
- Create: `internal/middleware/accesslog.go`
- Create: `internal/middleware/recovery.go`
- Create: `internal/requestid/requestid.go`
- Create: `internal/buildinfo/buildinfo.go`
- Create: `internal/config/config.go`
- Create: `internal/migration/service.go`
- Create: `internal/worker/worker.go`
- Create: `internal/graphclient/client.go`

- [x] **Step 1: Wire app dependencies**

`app.New` loads config, creates the migration service, registers HTTP routes, and exposes `Run(ctx)`.

- [x] **Step 2: Register routes**

Expose `GET /healthz`, `GET /version`, and `POST /api/v1/hook/password`.

- [x] **Step 3: Keep future integrations explicit**

Define small interfaces for enqueueing password sync jobs and Graph user operations; provide no-op scaffold implementations only where needed for compile-time wiring.

- [x] **Step 4: Run all tests**

Run: `docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./...`

Expected: PASS.

### Task 4: Repository Structure and Tooling

**Files:**
- Create: `.gitignore`
- Create: `README.md`
- Create: `deploy/Dockerfile`
- Create: `deploy/docker-compose.yml`
- Create: `deploy/terraform/main.tf`
- Create: `deploy/terraform/variables.tf`
- Create: `deploy/terraform/outputs.tf`
- Create: `deploy/terraform/modules/aca/main.tf`
- Create: `deploy/terraform/modules/servicebus/main.tf`
- Create: `deploy/terraform/modules/keyvault/main.tf`
- Create: `.github/workflows/ci.yml`
- Create: `.github/workflows/cd.yml`

- [x] **Step 1: Add deploy/documentation placeholders**

Create files matching the approved design tree with comments that identify ownership and follow-up implementation scope.

- [x] **Step 2: Format and verify**

Run: `docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 sh -c "gofmt -w . && go test ./... && go vet ./..."`

Expected: PASS.

### Self-Review

- Spec coverage: This plan implements the design document's Project Structure section and the M1 foundation slice: Go module, health/version routes, HMAC middleware, RFC 9457 problem package, logger masking helper, and migration identity primitives.
- Deferred by design: Real Azure Service Bus, Graph API, Key Vault, Terraform resources, CI scanner installation, and PHP portal integration guide belong to later milestones in the approved design.
- Placeholder scan: The only placeholder files are deployment/Terraform scaffolds whose comments explicitly mark later milestone ownership; no runtime Go package contains placeholder behavior required for the M1 scaffold to compile and test.
