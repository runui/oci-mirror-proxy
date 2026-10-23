package app

import (
	"context"

	"github.com/runui/oci-mirror-proxy/internal/config"
)

// Fragment operations persist bytes only. Reload owns semantic validation and
// runtime publication, so invalid disk documents remain repairable through IPC.
func (s *Service) ListFragments() ([]config.Fragment, error) {
	return (config.FragmentStore{Main: s.path}).List()
}

func (s *Service) ReadFragment(name string) (config.Fragment, error) {
	return (config.FragmentStore{Main: s.path}).Read(name)
}

func (s *Service) WriteFragment(ctx context.Context, name, revision string, data []byte) (config.Fragment, error) {
	return (config.FragmentStore{Main: s.path}).Write(ctx, name, revision, data)
}
