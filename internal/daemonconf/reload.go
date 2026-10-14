package daemonconf

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ErrReloadUnsupported is returned on platforms without SIGHUP.
var ErrReloadUnsupported = errors.New("dockerd reload is only supported on unix systems; restart dockerd manually")

// Reload asks the dockerd process recorded in pidFile to re-read its configuration.
func Reload(pidFile string) error {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return fmt.Errorf("read dockerd pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return fmt.Errorf("pid file %s does not contain a valid process id", pidFile)
	}
	return sendReload(pid)
}
