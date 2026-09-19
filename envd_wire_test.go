package e2b

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	filesystempb "github.com/godeps/go-e2b/internal/gen/envd/filesystem"
	processpb "github.com/godeps/go-e2b/internal/gen/envd/process"
)

// These tests cover the two defects that made every call against a real
// deployment fail, both of which the rest of the suite could not see because it
// only ever talked to Connect handlers and to fixtures built from the SDK's own
// types:
//
//   - the clients negotiated the protobuf codec, which envd rejects outright;
//   - FileInfo could not decode the numeric "type" that envd's /files route
//     sends.
//
// The fixtures below therefore use bytes captured from a live deployment rather
// than values re-encoded by the SDK.

// recordingTransport captures the requests a sandbox sends. Connect handlers
// decode either codec, so only the bytes actually on the wire can catch a wrong
// one.
type recordingTransport struct {
	inner    http.RoundTripper
	requests []recordedRequest
}

type recordedRequest struct {
	method      string
	url         string
	contentType string
	body        []byte
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	r.requests = append(r.requests, recordedRequest{
		method:      req.Method,
		url:         req.URL.String(),
		contentType: req.Header.Get("Content-Type"),
		body:        body,
	})
	return r.inner.RoundTrip(req)
}

// only returns the single request the test expects, failing if the call did not
// produce exactly one.
func (r *recordingTransport) only(t *testing.T) recordedRequest {
	t.Helper()
	if len(r.requests) != 1 {
		t.Fatalf("recorded %d requests, want exactly 1: %+v", len(r.requests), r.requests)
	}
	return r.requests[0]
}

// recordRequests routes a sandbox's traffic through a recording transport.
func recordRequests(sbx *Sandbox) *recordingTransport {
	rec := &recordingTransport{inner: sbx.client.httpClient.Transport}
	sbx.client.httpClient.Transport = rec
	return rec
}

// jsonPayload returns the JSON a Connect request carries, unwrapping the
// [flag][length][payload] envelope that streaming calls use. A protobuf body
// holds NUL and control bytes and so never passes json.Valid.
func jsonPayload(body []byte) []byte {
	const headerLen = 5
	if len(body) >= headerLen && (body[0] == 0 || body[0] == 2) {
		size := int(body[1])<<24 | int(body[2])<<16 | int(body[3])<<8 | int(body[4])
		if headerLen+size <= len(body) {
			return body[headerLen : headerLen+size]
		}
	}
	return body
}

// assertProtoJSON fails when a recorded request did not go out as proto-JSON.
// envd parses every request body as JSON and ignores Content-Type, so a
// protobuf body comes back as
// `400 Bad Request: invalid character '\x1c' looking for beginning of value`.
//
// Connect derives the content type from the codec: streaming RPCs use
// application/connect+json and unary RPCs application/json, so want is the one
// that matches the call shape under test.
func assertProtoJSON(t *testing.T, label string, req recordedRequest, want string) {
	t.Helper()
	if req.contentType != want {
		t.Errorf("%s Content-Type = %q, want %q (envd parses every body as JSON)",
			label, req.contentType, want)
	}
	if payload := jsonPayload(req.body); !json.Valid(payload) {
		t.Errorf("%s body is not JSON: %q", label, payload)
	}
}

// newRawEnvdSandbox routes all envd traffic to a plain HTTP server so a test can
// replay the exact bytes a deployment sends. A Connect handler would re-encode
// responses with its own codec and hide wire-format differences.
func newRawEnvdSandbox(t *testing.T, handler http.Handler) *Sandbox {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	origTransport := srv.Client().Transport
	sbx := &Sandbox{
		ID:          "sbx-test",
		accessToken: "token-test",
		client: &Client{
			sandboxDomain: "test.e2b.app",
			httpClient: &http.Client{
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					req.URL.Scheme = "https"
					req.URL.Host = srv.Listener.Addr().String()
					return origTransport.RoundTrip(req)
				}),
			},
		},
	}
	sbx.Filesystem = newFilesystemService(sbx)
	sbx.Commands = newCommandService(sbx)
	return sbx
}

// --- codec ---

// TestCommandsRunSendsProtoJSON pins the codec the process client negotiates.
func TestCommandsRunSendsProtoJSON(t *testing.T) {
	sbx := newTestSandbox(t, func(_ context.Context, _ *connect.Request[processpb.StartRequest], stream *connect.ServerStream[processpb.StartResponse]) error {
		_ = sendStart(stream, startEvent(1))
		_ = sendStart(stream, stdoutEvent([]byte("hello\n")))
		_ = sendStart(stream, endEvent(0, true))
		return nil
	})
	rec := recordRequests(sbx)

	result, err := sbx.Commands.Run(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "hello\n")
	}

	req := rec.only(t)
	if !strings.Contains(req.url, "/process.Process/Start") {
		t.Errorf("url = %q, want the Process/Start route", req.url)
	}
	assertProtoJSON(t, "Process/Start", req, "application/connect+json")
}

// TestFilesystemListSendsProtoJSON pins the codec the filesystem client
// negotiates; it is built by a different constructor from the process client.
func TestFilesystemListSendsProtoJSON(t *testing.T) {
	sbx := newFilesystemRPCTestSandbox(t, &testFilesystemHandler{
		listFn: func(context.Context, *connect.Request[filesystempb.ListDirRequest]) (*connect.Response[filesystempb.ListDirResponse], error) {
			return connect.NewResponse(&filesystempb.ListDirResponse{Entries: []*filesystempb.EntryInfo{
				{Name: "a.txt", Path: "/home/user/a.txt", Type: filesystempb.FileType_FILE_TYPE_FILE},
			}}), nil
		},
	})
	rec := recordRequests(sbx)

	entries, err := sbx.Filesystem.List(context.Background(), "/home/user")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "a.txt" {
		t.Fatalf("entries = %+v, want one entry named a.txt", entries)
	}

	req := rec.only(t)
	if !strings.Contains(req.url, "/filesystem.Filesystem/ListDir") {
		t.Errorf("url = %q, want the Filesystem/ListDir route", req.url)
	}
	assertProtoJSON(t, "Filesystem/ListDir", req, "application/json")
}

// --- response decoding ---

// listDirResponse is the verbatim body a live deployment returned for
// Filesystem/ListDir. Two details matter: entries carry "type" as the numeric
// FileType ordinal rather than an enum name, and keys are camelCase
// (modifiedTime), neither of which the protobuf codec would produce.
const listDirResponse = `{"entries":[{"group":"1000","mode":493,"modifiedTime":"2026-09-19T22:49:50Z","name":"dir","owner":"1000","path":"/home/user/probe/dir","permissions":"drwxr-xr-x","size":4096,"type":2},{"group":"1000","mode":511,"modifiedTime":"2026-09-19T22:49:50Z","name":"link","owner":"1000","path":"/home/user/probe/link","permissions":"Lrwxrwxrwx","size":29,"type":1}]}`

// TestFilesystemListDecodesEnvdResponse replays a real ListDir body, so the
// client is held to what the deployment sends rather than to what a Connect
// handler chooses to emit. It doubles as a codec pin on the unary path: the
// binary codec would reject this response with
// `invalid content-type "application/json"; expecting "application/proto"`.
func TestFilesystemListDecodesEnvdResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/filesystem.Filesystem/ListDir", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listDirResponse))
	})
	sbx := newRawEnvdSandbox(t, mux)

	entries, err := sbx.Filesystem.List(context.Background(), "/home/user/probe")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}

	dir := entries[0]
	if dir.Name != "dir" || dir.Path != "/home/user/probe/dir" {
		t.Errorf("entry[0] = %+v, want the dir entry", dir)
	}
	if dir.Type != "directory" {
		t.Errorf("entry[0].Type = %q, want %q for type ordinal 2", dir.Type, "directory")
	}
	if dir.Size != 4096 || dir.Owner != "1000" || dir.Group != "1000" {
		t.Errorf("entry[0] metadata = %+v, want size 4096 owner/group 1000", dir)
	}
	if dir.ModTime.IsZero() {
		t.Error("entry[0].ModTime is zero, want the modifiedTime field decoded")
	}
	if entries[1].Type != "file" {
		t.Errorf("entry[1].Type = %q, want %q for type ordinal 1", entries[1].Type, "file")
	}
}

// TestFilesystemWriteDecodesEnvdResponse replays the verbatim body a live
// deployment returned for POST /files, where "type" is the numeric ordinal.
func TestFilesystemWriteDecodesEnvdResponse(t *testing.T) {
	const body = `[{"name":"new.txt","path":"/home/user/probe/dir/new.txt","type":1}]` + "\n"
	const path = "/home/user/probe/dir/new.txt"

	sbx := newRawEnvdSandbox(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != filesRoute {
			t.Errorf("path = %q, want %q", r.URL.Path, filesRoute)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(body))
	}))

	info, err := sbx.Filesystem.WriteString(context.Background(), path, "written")
	if err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if info.Name != "new.txt" || info.Path != path {
		t.Errorf("info = %+v, want name new.txt and path %s", info, path)
	}
	if info.Type != "file" {
		t.Errorf("Type = %q, want %q for type ordinal 1", info.Type, "file")
	}
}

// TestFileInfoUnmarshalJSON covers the spellings a /files response may use, since
// envd marshals its protobuf EntryInfo with encoding/json and so emits protobuf
// field names alongside numeric types.
func TestFileInfoUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantType string
		wantLink string
		wantMod  bool
	}{
		{name: "numeric file", body: `{"name":"a","path":"/a","type":1}`, wantType: "file"},
		{name: "numeric directory", body: `{"name":"a","path":"/a","type":2}`, wantType: "directory"},
		{name: "numeric symlink", body: `{"name":"a","path":"/a","type":3}`, wantType: "symlink"},
		{name: "numeric unspecified", body: `{"name":"a","path":"/a","type":0}`, wantType: "unknown"},
		{name: "type absent", body: `{"name":"a","path":"/a"}`, wantType: ""},
		{name: "enum name", body: `{"name":"a","path":"/a","type":"FILE_TYPE_DIRECTORY"}`, wantType: "directory"},
		{name: "short name", body: `{"name":"a","path":"/a","type":"file"}`, wantType: "file"},
		{name: "unknown name", body: `{"name":"a","path":"/a","type":"bogus"}`, wantType: "unknown"},
		{
			name:     "protobuf field names",
			body:     `{"name":"a","path":"/a","type":3,"symlink_target":"/target","modified_time":"2026-09-19T22:49:50Z"}`,
			wantType: "symlink",
			wantLink: "/target",
			wantMod:  true,
		},
		{
			name:     "json field names",
			body:     `{"name":"a","path":"/a","type":3,"symlinkTarget":"/target","modifiedTime":"2026-09-19T22:49:50Z"}`,
			wantType: "symlink",
			wantLink: "/target",
			wantMod:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var info FileInfo
			if err := json.Unmarshal([]byte(tt.body), &info); err != nil {
				t.Fatalf("Unmarshal(%s): %v", tt.body, err)
			}
			if info.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", info.Type, tt.wantType)
			}
			if info.SymlinkTarget != tt.wantLink {
				t.Errorf("SymlinkTarget = %q, want %q", info.SymlinkTarget, tt.wantLink)
			}
			if info.ModTime.IsZero() == tt.wantMod {
				t.Errorf("ModTime = %v, want decoded = %v", info.ModTime, tt.wantMod)
			}
		})
	}
}

// TestFileInfoUnmarshalJSONRejectsGarbage keeps the custom decoder from turning a
// malformed body into a silently empty FileInfo.
func TestFileInfoUnmarshalJSONRejectsGarbage(t *testing.T) {
	var info FileInfo
	if err := json.Unmarshal([]byte("not json"), &info); err == nil {
		t.Fatal("expected an error decoding a non-JSON body")
	}
}