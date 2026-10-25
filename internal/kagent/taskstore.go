// Package kagent implements the parts of the kagent runtime contract that a
// non-ADK harness must satisfy to run behind the kagent A2A gateway.
//
// The gateway does not read task state from the runtime: it reads kagent's own
// PostgreSQL store. A runtime that only keeps tasks in memory therefore streams
// a correct answer and then fails the call, because the gateway cannot find the
// task it is supposed to publish. That failure is visible as:
//
//	Code: NotFound
//	Message: task not found
//	ErrorInfo reason TASK_NOT_FOUND
//
// The runtime's half of the contract is the private gRPC TaskStoreService:
// CreateTask, UpdateTask, GetTask, ListTasks and SettleTask. This package
// implements the a2a-go task store interface on top of that service so the SDK
// persists every task where the gateway can read it.
package kagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2apb "github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"github.com/a2aproject/a2a-go/v2/a2apb/v1/pbconv"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	sdktaskstore "github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	runtimev1 "github.com/dimetron/pi-go/internal/kagent/runtimev1"
)

// Runtime identity files, projected by the Substrate runtime and rebound on
// actor restore. The session ID is the actor name without its "session-" prefix.
const (
	// IdentityDir is the directory Substrate projects runtime identity into.
	IdentityDir = "/run/kagent/identity"
	// identityNameFile holds the actor name, e.g. "session-<uuid>".
	identityNameFile = "name"
)

// DispatchHeader carries the gateway's attempt fence to the runtime task store.
// Sending it with the first save lets the gateway prove the input it reserved a
// dispatch for was accepted, so a failed send can safely be retried.
const DispatchHeader = "x-kagent-dispatch-id"

// RuntimeActorTokenPlaceholder is sent as the Authorization bearer value on
// every runtime RPC. Substrate egress rewrites that header at the control-plane
// origin into a short-lived Actor JWT for the kagent-runtime audience, so the
// value is never a usable credential of its own. kagent rejects runtime calls
// that omit the header, which is why a placeholder is sent rather than nothing.
const RuntimeActorTokenPlaceholder = "placeholder"

// Config configures a Client.
type Config struct {
	// Address is the kagent API gRPC address, e.g. "kagent-controller:8083".
	Address string
	// IdentityDir is the Substrate identity directory. Default IdentityDir.
	IdentityDir string
	// SessionID overrides the session ID read from the identity directory. Only
	// tests and offline tooling should set it.
	SessionID string
	// DialOptions are appended to the default dial options.
	DialOptions []grpc.DialOption
}

// Client is an a2a-go task store backed by kagent's private TaskStoreService.
// It keeps no local copy of task state, so every read reflects what the gateway
// will publish.
type Client struct {
	store   runtimev1.TaskStoreServiceClient
	conn    *grpc.ClientConn
	idDir   string
	session string

	// versions records the most recent committed version per task, so Settle
	// can publish the boundary without the executor having to observe return
	// values the SDK keeps to itself.
	versions sync.Map // a2a.TaskID -> sdktaskstore.TaskVersion
}

// recordVersion remembers the latest committed version for a task.
func (c *Client) recordVersion(id a2a.TaskID, version sdktaskstore.TaskVersion) {
	if id == "" || version <= 0 {
		return
	}
	c.versions.Store(id, version)
}

// LastVersion returns the most recent committed version recorded for taskID.
func (c *Client) LastVersion(id a2a.TaskID) sdktaskstore.TaskVersion {
	if v, ok := c.versions.Load(id); ok {
		if version, ok := v.(sdktaskstore.TaskVersion); ok {
			return version
		}
	}
	return sdktaskstore.TaskVersionMissing
}

var _ sdktaskstore.Store = (*Client)(nil)

// New dials the kagent API and returns a task store bound to one runtime actor.
//
// The connection is lazy: grpc.NewClient does not connect until the first RPC,
// so New succeeds even while the controller is starting.
func New(cfg Config) (*Client, error) {
	if cfg.Address == "" {
		return nil, errors.New("kagent task store: address is required")
	}
	dir := cfg.IdentityDir
	if dir == "" {
		dir = IdentityDir
	}
	opts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, cfg.DialOptions...)
	conn, err := grpc.NewClient(cfg.Address, opts...)
	if err != nil {
		return nil, fmt.Errorf("kagent task store: dial %s: %w", cfg.Address, err)
	}
	return &Client{
		store:   runtimev1.NewTaskStoreServiceClient(conn),
		conn:    conn,
		idDir:   dir,
		session: cfg.SessionID,
	}, nil
}

// Close releases the gRPC connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// SessionID returns the session this client persists under. The value is reread
// from the identity files on every call because a Substrate restore rebinds them
// to a new actor.
func (c *Client) SessionID(ctx context.Context) (string, error) {
	if c.session != "" {
		return c.session, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(c.idDir, identityNameFile))
	if err != nil {
		return "", fmt.Errorf("kagent task store: read runtime identity: %w", err)
	}
	name := strings.TrimSpace(string(raw))
	id, ok := strings.CutPrefix(name, "session-")
	if !ok {
		return "", fmt.Errorf("kagent task store: unexpected runtime actor name %q", name)
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("kagent task store: invalid runtime session identity: %w", err)
	}
	return id, nil
}

// callContext attaches the runtime credential the task store RPCs require. It
// deliberately drops caller credentials: these calls are made with workload
// authority, not a user's.
func (c *Client) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("x-user-id")
	md.Delete("x-agent-name")
	md.Delete("x-share-token")
	// Substrate replaces this placeholder with the calling Actor's JWT at the
	// control-plane origin, including after a restore or fork. kagent requires
	// the header to be present, so an empty value fails authentication.
	md.Set("authorization", "Bearer "+RuntimeActorTokenPlaceholder)
	return metadata.NewOutgoingContext(ctx, md), cancel
}

// Create persists a new task and returns its committed version. The gateway's
// dispatch fence is attached when present so the send counts as accepted.
func (c *Client) Create(ctx context.Context, task *a2a.Task) (sdktaskstore.TaskVersion, error) {
	wire, err := pbconv.ToProtoTask(task)
	if err != nil {
		return 0, fmt.Errorf("kagent task store: encode task: %w", err)
	}
	session, err := c.SessionID(ctx)
	if err != nil {
		return 0, err
	}
	req := &runtimev1.TaskStoreServiceCreateTaskRequest{SessionId: session, Task: wire}
	if id := dispatchID(ctx); id != "" {
		req.DispatchId = &id
	}
	var resp *runtimev1.TaskStoreServiceCreateTaskResponse
	if err := c.retry(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.store.CreateTask(ctx, req)
		return err
	}); err != nil {
		return 0, sdkError("create", err)
	}
	version := sdktaskstore.TaskVersion(resp.Version)
	c.recordVersion(task.ID, version)
	return version, nil
}

// Update saves the task at the expected version. Optimistic concurrency is
// enforced server-side, so a stale version surfaces as ErrConcurrentModification.
func (c *Client) Update(ctx context.Context, update *sdktaskstore.UpdateRequest) (sdktaskstore.TaskVersion, error) {
	if update == nil || update.Task == nil {
		return 0, errors.New("kagent task store: update requires a task")
	}
	task, err := pbconv.ToProtoTask(update.Task)
	if err != nil {
		return 0, fmt.Errorf("kagent task store: encode task: %w", err)
	}
	var event *a2apb.StreamResponse
	if update.Event != nil {
		wire, err := pbconv.ToProtoStreamResponse(update.Event)
		if err != nil {
			return 0, fmt.Errorf("kagent task store: encode event: %w", err)
		}
		event = wire
	}
	session, err := c.SessionID(ctx)
	if err != nil {
		return 0, err
	}
	req := &runtimev1.TaskStoreServiceUpdateTaskRequest{
		SessionId:       session,
		Task:            task,
		Event:           event,
		ExpectedVersion: int64(update.PrevVersion),
	}
	if id := dispatchID(ctx); id != "" {
		req.DispatchId = &id
	}
	var resp *runtimev1.TaskStoreServiceUpdateTaskResponse
	if err := c.retry(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.store.UpdateTask(ctx, req)
		return err
	}); err != nil {
		return 0, sdkError("update", err)
	}
	version := sdktaskstore.TaskVersion(resp.Version)
	c.recordVersion(update.Task.ID, version)
	return version, nil
}

// Get loads one stored task.
func (c *Client) Get(ctx context.Context, taskID a2a.TaskID) (*sdktaskstore.StoredTask, error) {
	session, err := c.SessionID(ctx)
	if err != nil {
		return nil, err
	}
	var resp *runtimev1.TaskStoreServiceGetTaskResponse
	if err := c.retry(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.store.GetTask(ctx, &runtimev1.TaskStoreServiceGetTaskRequest{
			SessionId: session,
			TaskId:    string(taskID),
		})
		return err
	}); err != nil {
		return nil, sdkError("get", err)
	}
	return fromStored(resp.GetStored())
}

// List returns the session's tasks matching the request.
func (c *Client) List(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	wire, err := pbconv.ToProtoListTasksRequest(req)
	if err != nil {
		return nil, fmt.Errorf("kagent task store: encode list request: %w", err)
	}
	session, err := c.SessionID(ctx)
	if err != nil {
		return nil, err
	}
	var resp *runtimev1.TaskStoreServiceListTasksResponse
	if err := c.retry(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.store.ListTasks(ctx, &runtimev1.TaskStoreServiceListTasksRequest{
			SessionId: session,
			Request:   wire,
		})
		return err
	}); err != nil {
		return nil, sdkError("list", err)
	}
	return pbconv.FromProtoListTasksResponse(resp.GetResult())
}

// Settle acknowledges that native execution has stopped at the saved version and
// publishes the task state and history atomically. Without it the gateway keeps
// a staged boundary private and the caller never sees the final result, so this
// is the call that makes a completed turn readable.
func (c *Client) Settle(ctx context.Context, taskID a2a.TaskID, version sdktaskstore.TaskVersion) error {
	if version <= 0 {
		// Callers that do not track versions rely on the latest one recorded
		// by Create or Update.
		version = c.LastVersion(taskID)
	}
	if version <= 0 {
		// Nothing was saved, so there is no boundary to publish.
		return nil
	}
	session, err := c.SessionID(ctx)
	if err != nil {
		return err
	}
	req := &runtimev1.TaskStoreServiceSettleTaskRequest{
		SessionId: session,
		TaskId:    string(taskID),
		Version:   int64(version),
	}
	if err := c.retry(ctx, func(ctx context.Context) error {
		_, err := c.store.SettleTask(ctx, req)
		return err
	}); err != nil {
		return sdkError("settle", err)
	}
	return nil
}

// retry reissues an identical mutation after a lost response. It never retries a
// conflict, never advances a version locally, and never reruns the agent.
func (c *Client) retry(ctx context.Context, call func(context.Context) error) error {
	const attempts = 4
	var err error
	for attempt := range attempts {
		var callCtx context.Context
		var cancel context.CancelFunc
		callCtx, cancel = c.callContext(ctx)
		err = call(callCtx)
		cancel()
		if code := status.Code(err); (code != codes.Unavailable && code != codes.DeadlineExceeded) || attempt == attempts-1 {
			return err
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

// fromStored converts a stored task, rejecting the zero value the service returns
// for a task that does not exist.
func fromStored(stored *runtimev1.StoredTask) (*sdktaskstore.StoredTask, error) {
	if stored == nil || stored.GetTask() == nil || stored.GetVersion() <= 0 {
		return nil, fmt.Errorf("kagent task store: invalid stored task: %w", a2a.ErrTaskNotFound)
	}
	task, err := pbconv.FromProtoTask(stored.GetTask())
	if err != nil {
		return nil, fmt.Errorf("kagent task store: decode task: %w", err)
	}
	return &sdktaskstore.StoredTask{
		Task:    task,
		Version: sdktaskstore.TaskVersion(stored.GetVersion()),
	}, nil
}

// sdkError maps a gRPC status onto the sentinel errors the SDK matches, so the
// handler layer can tell a missing task from a conflict or a bad request.
func sdkError(op string, err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return fmt.Errorf("kagent task store: %s: %w", op, a2a.ErrTaskNotFound)
	case codes.AlreadyExists:
		return fmt.Errorf("kagent task store: %s: %w", op, sdktaskstore.ErrTaskAlreadyExists)
	case codes.Aborted:
		return fmt.Errorf("kagent task store: %s: %w", op, sdktaskstore.ErrConcurrentModification)
	case codes.PermissionDenied, codes.Unauthenticated:
		return fmt.Errorf("kagent task store: %s: %w", op, a2a.ErrUnauthorized)
	case codes.InvalidArgument, codes.FailedPrecondition:
		return a2a.NewError(a2a.ErrInvalidParams, status.Convert(err).Message())
	default:
		return fmt.Errorf("kagent task store: %s: %w", op, err)
	}
}

// dispatchKey carries the gateway's dispatch fence from the request that started
// a turn to the store calls that turn makes.
type dispatchKey struct{}

// WithDispatchID records the gateway's dispatch fence on ctx.
func WithDispatchID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, dispatchKey{}, id)
}

// dispatchID returns the dispatch fence recorded on ctx, or "".
func dispatchID(ctx context.Context) string {
	id, _ := ctx.Value(dispatchKey{}).(string)
	return id
}

// DispatchInterceptor records the gateway's dispatch fence for the turn.
//
// Every save carries the fence so the gateway can prove that the input it
// reserved a dispatch for was accepted, which is what makes a failed send
// retryable instead of ambiguous. The fence arrives as a service param on the
// inbound A2A call and is copied onto the executor context, so Before is the one
// place that can read it.
type DispatchInterceptor struct {
	a2asrv.PassthroughCallInterceptor
}

// Before implements a2asrv.CallInterceptor.
func (DispatchInterceptor) Before(ctx context.Context, callCtx *a2asrv.CallContext, _ *a2asrv.Request) (context.Context, any, error) {
	if callCtx == nil {
		return ctx, nil, nil
	}
	if values, ok := callCtx.ServiceParams().Get(DispatchHeader); ok && len(values) == 1 {
		ctx = WithDispatchID(ctx, values[0])
	}
	return ctx, nil, nil
}
