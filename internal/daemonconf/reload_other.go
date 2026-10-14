//go:build !unix

package daemonconf

func sendReload(int) error {
	return ErrReloadUnsupported
}
