package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/noah-isme/backend-toko/internal/common"
)

// HTTPRecorder records HTTP requests after they have been handled.
type HTTPRecorder struct {
	Service   *Service
	OnError   func(error)
	ActorFunc func(*http.Request) Actor
}

// HTTPConfig customises how the audit entry is produced for a route.
type HTTPConfig struct {
	Action          string
	ResourceType    string
	ResourceIDParam string
	MetadataFunc    func(*http.Request, int) map[string]any
	ActorFunc       func(*http.Request) Actor
}

// Middleware returns a chi-compatible middleware that records audit entries.
func (r HTTPRecorder) Middleware(cfg HTTPConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if r.Service == nil || !r.Service.Enabled {
				next.ServeHTTP(w, req)
				return
			}

			holder := &annotations{}
			req = req.WithContext(context.WithValue(req.Context(), annotationsKey{}, holder))
			recorder := &statusRecorder{ResponseWriter: w, status: 0}
			next.ServeHTTP(recorder, req)

			actor := r.actor(req)
			if cfg.ActorFunc != nil {
				actor = cfg.ActorFunc(req)
			}

			resourceID := ""
			if cfg.ResourceIDParam != "" {
				resourceID = chi.URLParam(req, cfg.ResourceIDParam)
			}

			var payload map[string]any
			if cfg.MetadataFunc != nil {
				payload = cfg.MetadataFunc(req, recorder.Status())
			}
			// Handler annotations are merged regardless of the response status
			// (a failed staff change still records who was targeted) and win on
			// key collisions.
			payload = holder.mergeInto(payload)
			var metadata []byte
			if payload != nil {
				if data, err := json.Marshal(payload); err == nil {
					metadata = data
				}
			}

			if err := r.Service.Record(req.Context(), actor, cfg.Action, cfg.ResourceType, resourceID, req, recorder.Status(), metadata); err != nil && r.OnError != nil {
				r.OnError(err)
			}
		})
	}
}

func (r HTTPRecorder) actor(req *http.Request) Actor {
	if r.ActorFunc != nil {
		return r.ActorFunc(req)
	}
	if req == nil {
		return Actor{Kind: ActorKindAnonymous}
	}
	if userID, ok := common.UserID(req.Context()); ok && userID != "" {
		return Actor{Kind: ActorKindUser, UserID: &userID}
	}
	return Actor{Kind: ActorKindAnonymous}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Status() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	return s.ResponseWriter.Write(b)
}

type annotationsKey struct{}

type annotations struct {
	mu     sync.Mutex
	values map[string]any
}

func (a *annotations) mergeInto(payload map[string]any) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.values) == 0 {
		return payload
	}
	if payload == nil {
		payload = make(map[string]any, len(a.values))
	}
	for k, v := range a.values {
		payload[k] = v
	}
	return payload
}

// Annotate attaches key/value metadata to the audit entry recorded by the
// enclosing HTTPRecorder middleware for this request. It is a no-op when the
// request is not audited (no middleware, or auditing disabled).
func Annotate(ctx context.Context, key string, value any) {
	if ctx == nil || key == "" {
		return
	}
	holder, ok := ctx.Value(annotationsKey{}).(*annotations)
	if !ok || holder == nil {
		return
	}
	holder.mu.Lock()
	defer holder.mu.Unlock()
	if holder.values == nil {
		holder.values = map[string]any{}
	}
	holder.values[key] = value
}
