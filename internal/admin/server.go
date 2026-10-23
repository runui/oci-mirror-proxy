package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// JSON base64 encoding of a maximum-sized fragment needs more than 1 MiB.
const maxRequestBody = 2 << 20

// NewHTTPServer returns an HTTP server that serves the admin API. The caller
// supplies the listener, usually a unix socket from Listen.
func NewHTTPServer(ctl Controller) *http.Server {
	s := &server{ctl: ctl}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", s.status)
	mux.HandleFunc("GET /v1/info", func(w http.ResponseWriter, r *http.Request) {
		// Unsupported information must not be synthesized from status or the
		// caller's version. Older controller implementations remain usable.
		provider, ok := ctl.(interface{ Info() ServerInfo })
		if !ok {
			writeJSON(w, http.StatusNotImplemented, errorResponse{Error: "server information is not supported"})
			return
		}
		writeJSON(w, http.StatusOK, provider.Info())
	})
	mux.HandleFunc("GET /v1/registries/{alias}/upstreams", s.listUpstreams)
	mux.HandleFunc("POST /v1/registries/{alias}/upstreams", s.addUpstream)
	mux.HandleFunc("PATCH /v1/registries/{alias}/upstreams/{id}", s.patchUpstream)
	mux.HandleFunc("DELETE /v1/registries/{alias}/upstreams/{id}", s.removeUpstream)
	mux.HandleFunc("POST /v1/reload", s.reload)
	mux.HandleFunc("GET /v1/config/fragments", s.fragments)
	mux.HandleFunc("GET /v1/config/fragments/{name}", s.fragments)
	mux.HandleFunc("PUT /v1/config/fragments/{name}", s.fragments)
	mux.HandleFunc("DELETE /v1/config/fragments/{name}", s.fragments)
	return &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
}

type server struct {
	ctl Controller
}

type errorResponse struct {
	Error string `json:"error"`
}

func (s *server) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.ctl.Status())
}

func (s *server) listUpstreams(w http.ResponseWriter, r *http.Request) {
	ups, err := s.ctl.ListUpstreams(r.PathValue("alias"))
	if err != nil {
		writeErr(w, err)
		return
	}
	specs := make([]UpstreamSpec, 0, len(ups))
	for _, u := range ups {
		specs = append(specs, SpecFromConfig(u))
	}
	writeJSON(w, http.StatusOK, specs)
}

func (s *server) addUpstream(w http.ResponseWriter, r *http.Request) {
	var spec UpstreamSpec
	if !decodeBody(w, r, &spec) {
		return
	}
	u, err := spec.ToConfig()
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.ctl.AddUpstream(r.PathValue("alias"), u); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *server) patchUpstream(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Disabled *bool `json:"disabled"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Disabled == nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `body must contain "disabled"`})
		return
	}
	if err := s.ctl.SetUpstreamDisabled(r.PathValue("alias"), r.PathValue("id"), *body.Disabled); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) removeUpstream(w http.ResponseWriter, r *http.Request) {
	if err := s.ctl.RemoveUpstream(r.PathValue("alias"), r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) reload(w http.ResponseWriter, _ *http.Request) {
	if err := s.ctl.Reload(); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodeBody strictly decodes a JSON body. Unknown fields are rejected, so a
// misspelt option fails loudly instead of being silently ignored.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body: " + err.Error()})
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "request must contain exactly one JSON value"})
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
