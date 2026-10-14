//go:build unix

package daemonconf

import "syscall"

func sendReload(pid int) error {
	return syscall.Kill(pid, syscall.SIGHUP)
}
