package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

// FragmentController is optional so existing controllers and API consumers keep
// working. It exposes raw config.d storage, never subscription operations.
type FragmentController interface {
	ListFragments() ([]config.Fragment, error)
	ReadFragment(string) (config.Fragment, error)
	WriteFragment(context.Context, string, string, []byte) (config.Fragment, error)
}

type fragmentMutation struct {
	Revision *string `json:"revision"`
	Data     []byte  `json:"data"`
}

func (s *server) fragments(w http.ResponseWriter, r *http.Request) {
	ctl, ok := s.ctl.(FragmentController)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, errorResponse{Error: "configuration fragments are not supported"})
		return
	}
	var out any
	var err error
	w.Header().Set("Cache-Control", "no-store")
	name := r.PathValue("name")
	switch {
	case r.Method == http.MethodGet && name == "":
		out, err = ctl.ListFragments()
	case r.Method == http.MethodGet:
		out, err = ctl.ReadFragment(name)
	default:
		var body fragmentMutation
		if !decodeBody(w, r, &body) {
			return
		}
		if body.Revision == nil {
			writeJSON(w, http.StatusPreconditionRequired, errorResponse{Error: "revision is required"})
			return
		}
		if r.Method == http.MethodPut && body.Data == nil {
			body.Data = []byte{}
		}
		if r.Method == http.MethodDelete {
			body.Data = nil
		}
		out, err = ctl.WriteFragment(r.Context(), name, *body.Revision, body.Data)
	}
	if errors.Is(err, config.ErrInvalidFragment) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if errors.Is(err, config.ErrFragmentConflict) {
		writeJSON(w, http.StatusPreconditionFailed, errorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (c *Client) ListFragments() ([]config.Fragment, error) {
	var out []config.Fragment
	err := c.do(http.MethodGet, "/v1/config/fragments", nil, &out)
	return out, err
}

func (c *Client) ReadFragment(name string) (config.Fragment, error) {
	var out config.Fragment
	err := c.do(http.MethodGet, "/v1/config/fragments/"+url.PathEscape(name), nil, &out)
	return out, err
}

func (c *Client) WriteFragment(name, revision string, data []byte) (config.Fragment, error) {
	method := http.MethodPut
	if data == nil {
		method = http.MethodDelete
	}
	var out config.Fragment
	err := c.do(method, "/v1/config/fragments/"+url.PathEscape(name), fragmentMutation{Revision: &revision, Data: data}, &out)
	return out, err
}
