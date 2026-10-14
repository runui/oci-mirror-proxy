package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/admin"
)

const shutdownTimeout = 10 * time.Second

// Serve runs the registry API and the admin socket until ctx is cancelled or one
// of the servers fails. Blob transfers are long-lived, so no overall write
// timeout is set on the data plane.
func (s *Service) Serve(ctx context.Context, adminSocket string) error {
	dataLn, err := net.Listen("tcp", s.ListenAddr())
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.ListenAddr(), err)
	}
	adminLn, err := admin.Listen(adminSocket)
	if err != nil {
		_ = dataLn.Close()
		return err
	}

	dataSrv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	adminSrv := admin.NewHTTPServer(s)

	errc := make(chan error, 2)
	go func() { errc <- dataSrv.Serve(dataLn) }()
	go func() { errc <- adminSrv.Serve(adminLn) }()
	s.logger.Info("serving", "listen", s.ListenAddr(), "admin_socket", adminSocket)

	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = dataSrv.Shutdown(shutdownCtx)
	_ = adminSrv.Shutdown(shutdownCtx)
	s.Close()
	s.logger.Info("stopped")
	return serveErr
}
