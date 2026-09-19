# Development Guide

## Proto Bindings

The SDK communicates with the E2B sandbox daemon (`envd`) using the
[Connect RPC](https://connectrpc.com) protocol. The service is defined in
`proto/envd/process/process.proto` — a vendored copy of the upstream definition
from [e2b-dev/infra](https://github.com/e2b-dev/infra).

Generated Go code lives in `internal/gen/` and is committed to the repository.
**Do not edit files under `internal/gen/` by hand.**

---

## Wire codec

`envdClientOptions` in `sandbox.go` is the single place the codec is chosen, and
it selects proto-JSON for every envd service client. This is not a preference:
envd parses **every request body as JSON and ignores the request
`Content-Type`**, so Connect's default protobuf body — `\n\x1f\n\t/bin/bash...` —
is handed straight to the JSON parser and rejected with

```
400 Bad Request: invalid character '\x1c' looking for beginning of value
```

which fails every process and filesystem call. Responses are JSON either way.

`envd_wire_test.go` pins this on the wire for both the streaming and the unary
path. A test that talks to a Connect handler cannot catch a regression here:
handlers decode either codec, so only the bytes actually sent are decisive.

---

## One-time Setup

Install the three tools needed for code generation. These are only required
when you need to regenerate bindings — they are **not** needed to build or use
the SDK.

```sh
# buf — drives the code generation pipeline
brew install bufbuild/buf/buf          # macOS
# or: go install github.com/bufbuild/buf/cmd/buf@latest

# protoc-gen-go — generates Go protobuf message types (.pb.go)
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

# protoc-gen-connect-go — generates the typed Connect client (.connect.go)
go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
```

Verify all three are in your PATH:

```sh
buf --version
protoc-gen-go --version
protoc-gen-connect-go --version
```

---

## Regenerating Bindings

Use this when you have updated `proto/envd/process/process.proto` manually
and want to regenerate:

```sh
make generate
```

This runs `buf generate` and `go mod tidy`.

---

## Syncing proto from upstream (same commit)

To re-fetch the proto at the currently pinned commit and regenerate:

```sh
make proto-sync
```

The pinned commit is stored in `proto/envd/VERSION`.

---

## Upgrading to a newer upstream version

To pull the latest commit of `process.proto` from `e2b-dev/infra`, update the
pin, and regenerate everything:

```sh
make proto-upgrade
```

This will:
1. Query the GitHub API for the latest commit that touched `process.proto`
2. Write the new SHA to `proto/envd/VERSION`
3. Fetch the new proto
4. Regenerate bindings via `buf generate`

Review the diff carefully before committing — breaking changes in the proto
will appear as compile errors or changed generated types.

---

## Pinning a specific upstream commit manually

Edit `proto/envd/VERSION` to contain the desired commit SHA, then run:

```sh
make proto-sync
```

---

## Running tests / lint / security scan

```sh
make test    # go test ./... -race
make lint    # golangci-lint run ./...
make gosec   # gosec (excludes internal/gen)
```

---

## Running integration tests against a self-hosted deployment

Set `E2B_API_KEY` and `E2B_API_URL`; the integration tests skip when no key is
present. Bump the timeout, since the suite creates real sandboxes:

```sh
E2B_TEMPLATE=base go test -tags=integration -count=1 -timeout 25m ./...
```

Against an envd 0.5.2 deployment on Aliyun Function Compute
(`https://api.cn-shanghai.e2b.fc.aliyuncs.com`), these upstream features are not
available. Their tests fail there because of the deployment, not the SDK:

| Feature | What the deployment answers |
|---|---|
| pause / auto-pause / auto-resume | `400 enableAutoPause requires snapshot feature to be enabled` |
| template build (build, status, aliases, tags) | `400`, the build API is not enabled |
| volumes | not routed |
| sandbox list V2 | filters, ordering and pagination unsupported |
| signed URLs | `403 access denied: X-Access-Token header is required` — this envd wants the token even on signed URLs |

Three more fail because envd 0.5.2 does not match the test's expectation:

| Test | envd 0.5.2 behaviour |
|---|---|
| `TestIntegrationFilesystemListNotFound` | `ListDir` on a missing path returns `200 {"entries":[]}`; `Stat` does return NotFound |
| `TestIntegrationFilesystemSymlink` | `ListDir` omits `symlinkTarget` and reports the resolved type; the target is only visible through `Stat` |
| `TestIntegrationCommandsBackgroundListKill` | `SendSignal` on a dead pid succeeds rather than returning NotFound, so a second `Kill` reports `true` |

`WithFileUser` is a no-op there for filesystem RPCs: the user is ignored whether
it travels as `X-User-ID` or as `Authorization: Basic`, and entries stay owned by
the sandbox's default user (uid 1000).

---

## File layout

```
proto/
  envd/
    VERSION                  ← pinned upstream commit SHA
    process/
      process.proto          ← vendored proto (DO NOT edit upstream options)
buf.yaml                     ← buf module config (proto root = proto/)
buf.gen.yaml                 ← buf generation config (output → internal/gen)
internal/
  gen/
    envd/
      process/
        process.pb.go        ← generated message types   (DO NOT EDIT)
        processconnect/
          process.connect.go ← generated Connect client  (DO NOT EDIT)
```

The `go_package` option in the vendored proto is set to
`github.com/godeps/go-e2b/internal/gen/envd/process` so the import
paths in generated code match the module layout. This option is injected by
`make proto-sync` if the upstream file does not carry it.
