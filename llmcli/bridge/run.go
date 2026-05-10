package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/php-workx/fabrikk/llmcli"
	"github.com/php-workx/fabrikk/llmcli/daemon"
	"github.com/php-workx/fabrikk/llmcli/internal"
	"github.com/php-workx/fabrikk/llmclient"
)

const maxWireLineBytes = 8 * 1024 * 1024

// Run starts the op-agnostic stdio JSON-lines bridge and blocks until input is
// closed or ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	if cfg.In == nil {
		cfg.In = os.Stdin
	}
	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}
	if len(cfg.BackendNames) == 0 {
		cfg.BackendNames = backendNamesFromEnv()
	}
	if cfg.LockfilePath == "" {
		cfg.LockfilePath = daemon.DefaultLockfilePath("llmcli")
	}

	lc := daemon.NewLifecycle(daemon.LifecycleConfig{ShutdownTimeout: cfg.ShutdownTimeout})
	return lc.Run(ctx, func(ctx context.Context) error {
		var lock *daemon.LockFile
		if !cfg.DisableLockfile {
			var err error
			lock, err = daemon.AcquireLockfile(cfg.LockfilePath)
			if err != nil {
				return err
			}
			defer lock.Release() //nolint:errcheck // best-effort shutdown cleanup
		}

		backend := cfg.Backend
		if backend == nil {
			var err error
			backend, err = llmcli.SelectBackendChain(ctx, cfg.BackendNames)
			if err != nil {
				return err
			}
		}
		defer backend.Close() //nolint:errcheck // best-effort shutdown cleanup

		r := &runner{
			in:      cfg.In,
			out:     cfg.Out,
			deps:    Deps{Backend: backend},
			active:  make(map[string]context.CancelFunc),
			writeCh: make(chan Response, 32),
		}
		return r.run(ctx)
	})
}

type runner struct {
	in      io.Reader
	out     io.Writer
	deps    Deps
	active  map[string]context.CancelFunc
	mu      sync.Mutex
	wg      sync.WaitGroup
	writeCh chan Response
}

func (r *runner) run(ctx context.Context) error {
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- writeResponses(r.out, r.writeCh)
	}()

	readErr := r.readLoop(ctx)
	if readErr != nil {
		r.cancelAll()
	}
	r.wg.Wait()
	close(r.writeCh)
	writeErr := <-writerDone
	if readErr != nil {
		return readErr
	}
	return writeErr
}

func (r *runner) readLoop(ctx context.Context) error {
	br := bufio.NewReader(r.in)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := internal.ReadBoundedLine(br, maxWireLineBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if errors.Is(err, internal.ErrLineTooLong) {
				return fmt.Errorf("bridge request exceeds %d bytes", maxWireLineBytes)
			}
			return err
		}
		if len(line) > 0 {
			r.handleLine(ctx, line)
		}
	}
}

func (r *runner) handleLine(ctx context.Context, line []byte) {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		r.writeCh <- Response{Final: true, Error: &WireError{Type: llmclient.ErrTypeBadRequest, Message: err.Error()}}
		return
	}
	if req.Cancel {
		r.cancel(req.RequestID)
		return
	}
	r.startRequest(ctx, req)
}

func (r *runner) startRequest(ctx context.Context, req Request) {
	if req.RequestID == "" {
		r.writeCh <- errorResponse(req.RequestID, llmclient.ErrTypeBadRequest, "request_id is required")
		return
	}
	h, ok := registeredHandler(req.Op)
	if !ok {
		r.writeCh <- errorResponse(req.RequestID, llmclient.ErrTypeBadRequest, fmt.Sprintf("unknown op %q", req.Op))
		return
	}
	reqCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.active[req.RequestID] = cancel
	r.mu.Unlock()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer r.forget(req.RequestID)
		defer cancel()
		result, err := h.Handle(reqCtx, req, r.deps)
		if err != nil {
			r.writeCh <- errorResponse(req.RequestID, classifyError(err), err.Error())
			return
		}
		r.writeCh <- Response{RequestID: req.RequestID, Final: true, Result: result}
	}()
}

func (r *runner) cancel(requestID string) {
	r.mu.Lock()
	cancel := r.active[requestID]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *runner) forget(requestID string) {
	r.mu.Lock()
	delete(r.active, requestID)
	r.mu.Unlock()
}

func (r *runner) cancelAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cancel := range r.active {
		cancel()
	}
}

func writeResponses(out io.Writer, ch <-chan Response) error {
	enc := json.NewEncoder(out)
	for resp := range ch {
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return nil
}

func errorResponse(requestID, typ, msg string) Response {
	return Response{RequestID: requestID, Final: true, Error: &WireError{Type: typ, Message: msg}}
}

func classifyError(err error) string {
	if errors.Is(err, context.Canceled) {
		return llmclient.ErrTypeCancelled
	}
	var eventErr *llmclient.EventErrorError
	if errors.As(err, &eventErr) && eventErr.Type != "" {
		return eventErr.Type
	}
	return llmclient.ErrTypeInternal
}
