package kagent

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2apb/v1/pbconv"
	sdktaskstore "github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	runtimev1 "github.com/dimetron/pi-go/internal/kagent/runtimev1"
)

// sessionID is the actor name Substrate projects, minus its "session-" prefix.
const (
	testSessionID = "01a14c52-2a3e-7d47-8524-6efd9f231527"
	testActorName = "session-" + testSessionID
)

// fakeStore records the RPCs the client issues and returns scripted responses.
type fakeStore struct {
	runtimev1.UnimplementedTaskStoreServiceServer

	createCalls, updateCalls, getCalls, settleCalls int

	// versions returned by Create/Update, keyed by call order.
	createVersion int64
	updateVersion int64

	// errs are returned instead of a success, when set.
	createErr, updateErr, getErr, settleErr error

	// lastReq captures the most recent request per method for assertions.
	lastCreate *runtimev1.TaskStoreServiceCreateTaskRequest
	lastUpdate *runtimev1.TaskStoreServiceUpdateTaskRequest
	lastSettle *runtimev1.TaskStoreServiceSettleTaskRequest
	lastGet    *runtimev1.TaskStoreServiceGetTaskRequest

	// stored is returned by GetTask when getErr is nil.
	stored *runtimev1.StoredTask
}

func (f *fakeStore) CreateTask(_ context.Context, req *runtimev1.TaskStoreServiceCreateTaskRequest) (*runtimev1.TaskStoreServiceCreateTaskResponse, error) {
	f.createCalls++
	f.lastCreate = req
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &runtimev1.TaskStoreServiceCreateTaskResponse{Version: f.createVersion}, nil
}

func (f *fakeStore) UpdateTask(_ context.Context, req *runtimev1.TaskStoreServiceUpdateTaskRequest) (*runtimev1.TaskStoreServiceUpdateTaskResponse, error) {
	f.updateCalls++
	f.lastUpdate = req
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return &runtimev1.TaskStoreServiceUpdateTaskResponse{Version: f.updateVersion}, nil
}

func (f *fakeStore) GetTask(_ context.Context, req *runtimev1.TaskStoreServiceGetTaskRequest) (*runtimev1.TaskStoreServiceGetTaskResponse, error) {
	f.getCalls++
	f.lastGet = req
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &runtimev1.TaskStoreServiceGetTaskResponse{Stored: f.stored}, nil
}

func (f *fakeStore) SettleTask(_ context.Context, req *runtimev1.TaskStoreServiceSettleTaskRequest) (*runtimev1.TaskStoreServiceSettleTaskResponse, error) {
	f.settleCalls++
	f.lastSettle = req
	if f.settleErr != nil {
		return nil, f.settleErr
	}
	return &runtimev1.TaskStoreServiceSettleTaskResponse{}, nil
}

// newTestClient starts fake on a bufconn listener and returns a Client bound to
// it. Identity is supplied through Config.SessionID, so no files are needed.
func newTestClient(t *testing.T, fake *fakeStore) *Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	runtimev1.RegisterTaskStoreServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	client, err := New(Config{
		Address:   "passthrough:///bufnet",
		SessionID: testSessionID,
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func testTask(t *testing.T) *a2a.Task {
	t.Helper()
	return &a2a.Task{
		ID:        "task-1",
		ContextID: testSessionID,
		Status:    a2a.TaskStatus{State: a2a.TaskStateSubmitted},
		History: []*a2a.Message{
			a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi")),
		},
	}
}

// The client must satisfy the SDK's store interface, or the A2A handler cannot
// be built with it.
func TestClientImplementsStore(t *testing.T) {
	var _ sdktaskstore.Store = (*Client)(nil)
}

func TestNewRequiresAddress(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New without an address should fail")
	}
}

// Create must send the session the gateway expects and return the version it
// reports, because the SDK threads that version into every later update.
func TestCreateSendsSessionAndReturnsVersion(t *testing.T) {
	fake := &fakeStore{createVersion: 7}
	client := newTestClient(t, fake)

	version, err := client.Create(context.Background(), testTask(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if version != 7 {
		t.Errorf("version = %d, want 7", version)
	}
	if fake.lastCreate.GetSessionId() != testSessionID {
		t.Errorf("session_id = %q, want %q", fake.lastCreate.GetSessionId(), testSessionID)
	}
	if fake.lastCreate.GetTask().GetId() != "task-1" {
		t.Errorf("task id = %q, want task-1", fake.lastCreate.GetTask().GetId())
	}
	if fake.lastCreate.DispatchId != nil {
		t.Errorf("dispatch_id = %v, want unset without a fence", *fake.lastCreate.DispatchId)
	}
}

// Update must forward the expected version: without it the gateway cannot
// enforce optimistic concurrency and a stale write would overwrite a boundary.
func TestUpdateForwardsExpectedVersion(t *testing.T) {
	fake := &fakeStore{updateVersion: 9}
	client := newTestClient(t, fake)

	version, err := client.Update(context.Background(), &sdktaskstore.UpdateRequest{
		Task:        testTask(t),
		PrevVersion: 7,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if version != 9 {
		t.Errorf("version = %d, want 9", version)
	}
	if fake.lastUpdate.GetExpectedVersion() != 7 {
		t.Errorf("expected_version = %d, want 7", fake.lastUpdate.GetExpectedVersion())
	}
}

func TestUpdateRejectsNilTask(t *testing.T) {
	client := newTestClient(t, &fakeStore{})
	if _, err := client.Update(context.Background(), &sdktaskstore.UpdateRequest{}); err == nil {
		t.Fatal("Update with no task should fail")
	}
}

// Settle is what publishes the boundary. A caller that tracked no version must
// still publish the latest one committed by Create or Update, otherwise the
// turn streams a correct answer and the call still fails.
func TestSettleUsesLastCommittedVersion(t *testing.T) {
	fake := &fakeStore{createVersion: 5}
	client := newTestClient(t, fake)

	task := testTask(t)
	if _, err := client.Create(context.Background(), task); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := client.Settle(context.Background(), task.ID, 0); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if fake.settleCalls != 1 {
		t.Fatalf("SettleTask calls = %d, want 1", fake.settleCalls)
	}
	if got := fake.lastSettle.GetVersion(); got != 5 {
		t.Errorf("settle version = %d, want 5", got)
	}
	if got := fake.lastSettle.GetTaskId(); got != "task-1" {
		t.Errorf("settle task_id = %q, want task-1", got)
	}
	if got := fake.lastSettle.GetSessionId(); got != testSessionID {
		t.Errorf("settle session_id = %q, want %q", got, testSessionID)
	}
}

// A task that was never saved has no boundary to publish. Settling it must be a
// no-op rather than a call that would be rejected.
func TestSettleSkipsUnknownTask(t *testing.T) {
	fake := &fakeStore{}
	client := newTestClient(t, fake)

	if err := client.Settle(context.Background(), "never-saved", 0); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if fake.settleCalls != 0 {
		t.Errorf("SettleTask calls = %d, want 0", fake.settleCalls)
	}
}

// Every store error must map onto the sentinel the SDK matches, or the handler
// reports an internal failure where the protocol calls for not-found, conflict,
// or a permission denial.
func TestSdkErrorMapsStatusCodes(t *testing.T) {
	tests := []struct {
		name   string
		code   codes.Code
		target error
	}{
		{"not found", codes.NotFound, a2a.ErrTaskNotFound},
		{"already exists", codes.AlreadyExists, sdktaskstore.ErrTaskAlreadyExists},
		{"aborted is a conflict", codes.Aborted, sdktaskstore.ErrConcurrentModification},
		{"permission denied", codes.PermissionDenied, a2a.ErrUnauthorized},
		{"unauthenticated", codes.Unauthenticated, a2a.ErrUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := sdkError("op", status.Error(tt.code, "boom"))
			if !errors.Is(err, tt.target) {
				t.Errorf("sdkError(%v) = %v, want it to wrap %v", tt.code, err, tt.target)
			}
		})
	}
}

// A NotFound from the server must surface as the SDK's not-found sentinel so the
// seed interceptor can still create the task instead of failing the turn.
func TestGetMapsNotFound(t *testing.T) {
	fake := &fakeStore{getErr: status.Error(codes.NotFound, "task not found")}
	client := newTestClient(t, fake)

	_, err := client.Get(context.Background(), "task-1")
	if !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("Get error = %v, want ErrTaskNotFound", err)
	}
	if fake.lastGet.GetTaskId() != "task-1" {
		t.Errorf("task_id = %q, want task-1", fake.lastGet.GetTaskId())
	}
}

// Get must round-trip the task and its version, which is what the SDK compares
// against on the next write.
func TestGetRoundTripsTask(t *testing.T) {
	wire, err := pbconv.ToProtoTask(testTask(t))
	if err != nil {
		t.Fatalf("ToProtoTask: %v", err)
	}
	fake := &fakeStore{stored: &runtimev1.StoredTask{Task: wire, Version: 3}}
	client := newTestClient(t, fake)

	stored, err := client.Get(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Version != 3 {
		t.Errorf("version = %d, want 3", stored.Version)
	}
	if stored.Task.ID != "task-1" {
		t.Errorf("task id = %q, want task-1", stored.Task.ID)
	}
}

// The dispatch fence makes a failed send retryable. It must reach the store on
// the save, and must be absent when no fence was recorded.
func TestDispatchFenceReachesCreate(t *testing.T) {
	fake := &fakeStore{createVersion: 1}
	client := newTestClient(t, fake)

	ctx := WithDispatchID(context.Background(), "dispatch-42")
	if _, err := client.Create(ctx, testTask(t)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := fake.lastCreate.GetDispatchId(); got != "dispatch-42" {
		t.Errorf("dispatch_id = %q, want dispatch-42", got)
	}
}

func TestDispatchIDEmptyIsNoop(t *testing.T) {
	ctx := context.Background()
	if got := WithDispatchID(ctx, ""); got != ctx {
		t.Error("WithDispatchID with an empty id should return the context unchanged")
	}
	if got := dispatchID(ctx); got != "" {
		t.Errorf("dispatchID = %q, want empty", got)
	}
}

// SessionID must come from the projected identity file and strip the
// "session-" prefix, because the gateway keys tasks by the bare UUID.
func TestSessionIDFromIdentityDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "name"), []byte(testActorName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := newTestClient(t, &fakeStore{})
	client.session = ""
	client.idDir = dir

	got, err := client.SessionID(context.Background())
	if err != nil {
		t.Fatalf("SessionID: %v", err)
	}
	if got != testSessionID {
		t.Errorf("SessionID = %q, want %q", got, testSessionID)
	}
}

// An actor name Substrate did not project in the expected form must be an error
// rather than a silently wrong session.
func TestSessionIDRejectsUnexpectedName(t *testing.T) {
	tests := []struct{ name, content string }{
		{"missing prefix", testSessionID},
		{"not a uuid", "session-not-a-uuid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "name"), []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			client := newTestClient(t, &fakeStore{})
			client.session = ""
			client.idDir = dir
			if _, err := client.SessionID(context.Background()); err == nil {
				t.Fatalf("SessionID(%q) should fail", tt.content)
			}
		})
	}
}

// Every runtime RPC must carry a bearer Authorization header, because Substrate
// egress rewrites that header into the Actor JWT and kagent rejects a call that
// omits it. Caller credentials must not leak into the workload call.
func TestCallContextCarriesRuntimeActorToken(t *testing.T) {
	client := newTestClient(t, &fakeStore{})

	ctx := metadata.NewIncomingContext(context.Background(), nil)
	ctx, cancel := client.callContext(ctx)
	defer cancel()

	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("callContext produced no outgoing metadata")
	}
	if got, want := md.Get("authorization"), "Bearer "+RuntimeActorTokenPlaceholder; len(got) != 1 || got[0] != want {
		t.Errorf("authorization = %v, want exactly [%q]", got, want)
	}
	for _, dropped := range []string{"x-user-id", "x-agent-name", "x-share-token"} {
		if got := md.Get(dropped); len(got) != 0 {
			t.Errorf("%s = %v, want caller credentials dropped", dropped, got)
		}
	}
}

// DispatchInterceptor must lift the fence off the inbound service params; before
// that it has nowhere to read the value from.
func TestDispatchInterceptorRecordsFence(t *testing.T) {
	interceptor := DispatchInterceptor{}
	ctx, _, err := interceptor.Before(context.Background(), nil, nil)
	if err != nil || ctx == nil {
		t.Fatalf("Before with a nil callCtx = (%v, %v), want passthrough", ctx, err)
	}
	if got := dispatchID(ctx); got != "" {
		t.Errorf("dispatchID = %q, want empty for a nil callCtx", got)
	}
}

// The stored task must carry an ID, or fromStored cannot distinguish an empty
// response from a real one.
func TestFromStoredRejectsEmpty(t *testing.T) {
	if _, err := fromStored(nil); err == nil {
		t.Fatal("fromStored(nil) should fail")
	}
	if _, err := fromStored(&runtimev1.StoredTask{}); err == nil {
		t.Fatal("fromStored with no task should fail")
	}
}
