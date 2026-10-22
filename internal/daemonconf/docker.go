package daemonconf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/unixhttp"
)

// containerdDriverPrefix is the driver-type prefix that containerd snapshotters report.
const containerdDriverPrefix = "io.containerd.snapshotter"

// Info is the subset of dockerd's GET /info response that this package reads.
type Info struct {
	OperatingSystem string          `json:"OperatingSystem"`
	DriverStatus    [][2]string     `json:"DriverStatus"`
	RegistryConfig  *RegistryConfig `json:"RegistryConfig"`
}

// RegistryConfig is the registry section of dockerd's /info response.
type RegistryConfig struct {
	Mirrors []string `json:"Mirrors"`
}

// FetchInfo queries dockerd's /info endpoint through its unix socket.
func FetchInfo(ctx context.Context, socket string) (Info, error) {
	client := unixhttp.Client(socket, 10*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/info", nil)
	if err != nil {
		return Info{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Info{}, fmt.Errorf("query dockerd at %s: %w", socket, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("dockerd /info answered %s", resp.Status)
	}
	var info Info
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&info); err != nil {
		return Info{}, fmt.Errorf("decode dockerd /info: %w", err)
	}
	return info, nil
}

// DetectStore reports which image store the daemon described by info uses.
// containerd snapshotters announce themselves through the driver-type entry.
func DetectStore(info Info) StoreKind {
	for _, kv := range info.DriverStatus {
		if kv[0] == "driver-type" && strings.HasPrefix(kv[1], containerdDriverPrefix) {
			return StoreContainerd
		}
	}
	return StoreLegacy
}
