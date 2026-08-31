package tools

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"github.com/a2aproject/a2a-go/v2/a2apb/v1/pbconv"
	"google.golang.org/protobuf/proto"
)

// grpcWebTransport implements a2aclient.Transport over the gRPC-Web wire
// protocol (application/grpc-web+proto). kagent's controller serves its A2A
// service over gRPC-Web on the HTTP port (:8083), routing each call to an
// AgentInstance via x-kagent-agent-instance-* headers. The a2a-go SDK ships
// JSON-RPC, REST, and native gRPC transports but no gRPC-Web transport, so
// this one fills that gap.
type grpcWebTransport struct {
	baseURL    string
	httpClient *http.Client
	// routing headers applied to every request (namespace + instance id).
	headers map[string]string
}

var _ a2aclient.Transport = (*grpcWebTransport)(nil)

// newGRPCWebTransport creates a transport for one AgentInstance.
func newGRPCWebTransport(baseURL string, headers map[string]string) *grpcWebTransport {
	return &grpcWebTransport{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{Timeout: 3 * time.Minute},
		headers:    headers,
	}
}

// grpcWebTransportFactory adapts newGRPCWebTransport to a2aclient.TransportFactory.
type grpcWebTransportFactory struct {
	baseURL string
	headers map[string]string
}

func (f *grpcWebTransportFactory) Create(context.Context, *a2a.AgentCard, *a2a.AgentInterface) (a2aclient.Transport, error) {
	return newGRPCWebTransport(f.baseURL, f.headers), nil
}

// errMissingGRPCWebTrailer reports a response that ended without the trailer
// frame carrying grpc-status, which is the only thing that makes a gRPC-Web
// response authoritatively complete.
var errMissingGRPCWebTrailer = errors.New("response ended without a gRPC-Web trailer")

// methodPath maps an A2A method name to the gRPC-Web URL path.
func methodPath(method string) string {
	return "/lf.a2a.v1.A2AService/" + method
}

// call performs one unary gRPC-Web call and returns the response payload.
func (t *grpcWebTransport) call(ctx context.Context, method string, req proto.Message, resp proto.Message) error {
	return t.callPath(ctx, methodPath(method), req, resp)
}

// callPath performs one unary gRPC-Web call to an explicit gRPC path (used for
// non-A2A services such as the kagent AgentInstanceService).
func (t *grpcWebTransport) callPath(ctx context.Context, path string, req proto.Message, resp proto.Message) error {
	body, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal %s request: %w", path, err)
	}
	frame := make([]byte, 5+len(body))
	frame[0] = 0 // data frame
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)))
	copy(frame[5:], body)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+path, bytes.NewReader(frame))
	if err != nil {
		return fmt.Errorf("create %s request: %w", path, err)
	}
	httpReq.Header.Set("Content-Type", "application/grpc-web+proto")
	httpReq.Header.Set("X-Grpc-Web", "1")
	httpReq.Header.Set("X-User-Agent", "grpc-web-javascript/0.1")
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}

	res, err := t.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", path, err)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d: %s", path, res.StatusCode, truncate(string(data), 300))
	}

	// Parse frames: data frames (flag 0) then a trailer frame (flag 0x80).
	// A valid protobuf message can marshal to zero bytes (e.g. an empty list
	// response), so an empty payload is not an error — only a missing data
	// frame is.
	var payload []byte
	sawData := false
	sawTrailer := false
	off := 0
	for off+5 <= len(data) {
		flag := data[off]
		length := binary.BigEndian.Uint32(data[off+1 : off+5])
		if off+5+int(length) > len(data) {
			return fmt.Errorf("%s: truncated frame", path)
		}
		chunk := data[off+5 : off+5+int(length)]
		if flag&0x80 != 0 {
			// Trailer frame carries grpc-status / grpc-message.
			status := parseTrailer(chunk)
			if status != "0" {
				return fmt.Errorf("%s: grpc-status %s", path, status)
			}
			sawTrailer = true
			break
		}
		sawData = true
		payload = append(payload, chunk...)
		off += 5 + int(length)
	}
	if !sawData {
		return fmt.Errorf("%s: empty response", path)
	}
	// A body that ends after its data frame never reported a status; taking
	// it as success would unmarshal a possibly-truncated payload.
	if !sawTrailer {
		return fmt.Errorf("%s: %w", path, errMissingGRPCWebTrailer)
	}
	if err := proto.Unmarshal(payload, resp); err != nil {
		return fmt.Errorf("unmarshal %s response: %w", path, err)
	}
	return nil
}

// parseTrailer extracts grpc-status from a gRPC-Web trailer frame.
func parseTrailer(trailer []byte) string {
	for _, line := range strings.Split(string(trailer), "\r\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "grpc-status" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// --- a2aclient.Transport implementation ---
func (t *grpcWebTransport) GetTask(ctx context.Context, params a2aclient.ServiceParams, req *a2a.GetTaskRequest) (*a2a.Task, error) {
	pbReq, err := pbconv.ToProtoGetTaskRequest(req)
	if err != nil {
		return nil, err
	}
	var pbResp a2apb.Task
	if err := t.call(ctx, "GetTask", pbReq, &pbResp); err != nil {
		return nil, err
	}
	return pbconv.FromProtoTask(&pbResp)
}

func (t *grpcWebTransport) ListTasks(ctx context.Context, params a2aclient.ServiceParams, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return nil, fmt.Errorf("ListTasks not implemented for kagent gRPC-Web transport")
}

func (t *grpcWebTransport) CancelTask(ctx context.Context, params a2aclient.ServiceParams, req *a2a.CancelTaskRequest) (*a2a.Task, error) {
	return nil, fmt.Errorf("CancelTask not implemented for kagent gRPC-Web transport")
}

func (t *grpcWebTransport) SendMessage(ctx context.Context, params a2aclient.ServiceParams, req *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	pbReq, err := pbconv.ToProtoSendMessageRequest(req)
	if err != nil {
		return nil, err
	}
	var pbResp a2apb.SendMessageResponse
	if err := t.call(ctx, "SendMessage", pbReq, &pbResp); err != nil {
		return nil, err
	}
	return pbconv.FromProtoSendMessageResponse(&pbResp)
}

func (t *grpcWebTransport) SubscribeToTask(ctx context.Context, params a2aclient.ServiceParams, req *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(nil, fmt.Errorf("SubscribeToTask not implemented for kagent gRPC-Web transport"))
	}
}

func (t *grpcWebTransport) SendStreamingMessage(ctx context.Context, params a2aclient.ServiceParams, req *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		pbReq, err := pbconv.ToProtoSendMessageRequest(req)
		if err != nil {
			yield(nil, err)
			return
		}
		body, err := proto.Marshal(pbReq)
		if err != nil {
			yield(nil, err)
			return
		}
		frame := make([]byte, 5+len(body))
		frame[0] = 0
		binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)))
		copy(frame[5:], body)

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+methodPath("SendStreamingMessage"), bytes.NewReader(frame))
		if err != nil {
			yield(nil, err)
			return
		}
		httpReq.Header.Set("Content-Type", "application/grpc-web+proto")
		httpReq.Header.Set("X-Grpc-Web", "1")
		httpReq.Header.Set("X-User-Agent", "grpc-web-javascript/0.1")
		for k, v := range t.headers {
			httpReq.Header.Set(k, v)
		}

		res, err := t.httpClient.Do(httpReq)
		if err != nil {
			yield(nil, err)
			return
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			yield(nil, fmt.Errorf("SendStreamingMessage: HTTP %d", res.StatusCode))
			return
		}

		// gRPC-Web streams each response as its own data frame; the final
		// frame is a trailer with grpc-status.
		reader := newFrameReader(res.Body)
		for {
			flag, payload, err := reader.Next()
			if errors.Is(err, io.EOF) {
				// The trailer carries the authoritative grpc-status, so a
				// stream that just stops is a truncated response, not a
				// successful one. Reporting it as success would hand back
				// whatever partial text arrived as if it were the whole
				// reply.
				yield(nil, errMissingGRPCWebTrailer)
				return
			}
			if err != nil {
				yield(nil, err)
				return
			}
			if flag&0x80 != 0 {
				if status := parseTrailer(payload); status != "0" {
					yield(nil, fmt.Errorf("SendStreamingMessage: grpc-status %s", status))
				}
				return
			}
			var pbResp a2apb.StreamResponse
			if err := proto.Unmarshal(payload, &pbResp); err != nil {
				yield(nil, err)
				return
			}
			event, err := pbconv.FromProtoStreamResponse(&pbResp)
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}

func (t *grpcWebTransport) GetTaskPushConfig(ctx context.Context, params a2aclient.ServiceParams, req *a2a.GetTaskPushConfigRequest) (*a2a.PushConfig, error) {
	return nil, fmt.Errorf("GetTaskPushConfig not implemented for kagent gRPC-Web transport")
}

func (t *grpcWebTransport) ListTaskPushConfigs(ctx context.Context, params a2aclient.ServiceParams, req *a2a.ListTaskPushConfigRequest) ([]*a2a.PushConfig, error) {
	return nil, fmt.Errorf("ListTaskPushConfigs not implemented for kagent gRPC-Web transport")
}

func (t *grpcWebTransport) CreateTaskPushConfig(ctx context.Context, params a2aclient.ServiceParams, req *a2a.PushConfig) (*a2a.PushConfig, error) {
	return nil, fmt.Errorf("CreateTaskPushConfig not implemented for kagent gRPC-Web transport")
}

func (t *grpcWebTransport) DeleteTaskPushConfig(ctx context.Context, params a2aclient.ServiceParams, req *a2a.DeleteTaskPushConfigRequest) error {
	return fmt.Errorf("DeleteTaskPushConfig not implemented for kagent gRPC-Web transport")
}

func (t *grpcWebTransport) GetExtendedAgentCard(ctx context.Context, params a2aclient.ServiceParams, req *a2a.GetExtendedAgentCardRequest) (*a2a.AgentCard, error) {
	return nil, fmt.Errorf("GetExtendedAgentCard not implemented for kagent gRPC-Web transport")
}

func (t *grpcWebTransport) Destroy() error {
	return nil
}

// frameReader reads gRPC-Web frames (1-byte flag + 4-byte length + payload)
// from a stream body.
type frameReader struct {
	r   io.Reader
	buf [5]byte
}

func newFrameReader(r io.Reader) *frameReader {
	return &frameReader{r: r}
}

// Next returns the next frame's flag and payload, or io.EOF at stream end.
func (f *frameReader) Next() (byte, []byte, error) {
	if _, err := io.ReadFull(f.r, f.buf[:]); err != nil {
		return 0, nil, err
	}
	flag := f.buf[0]
	length := binary.BigEndian.Uint32(f.buf[1:5])
	payload := make([]byte, length)
	if _, err := io.ReadFull(f.r, payload); err != nil {
		return 0, nil, err
	}
	return flag, payload, nil
}
