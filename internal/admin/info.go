package admin

import "runtime"

// BuildInfo identifies one process; client and server populate it independently.
type BuildInfo struct {
	Version   string `json:"version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"go_version"`
}

func CurrentBuild(version string) BuildInfo {
	return BuildInfo{Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version()}
}

// ConfigInfo is an allowlist, never a serialized config.Config. New internal
// configuration fields must not accidentally expose authentication or remotes.
type ConfigInfo struct {
	Listen     string         `json:"listen"`
	ProxyURL   string         `json:"proxy_url"`
	Registries []RegistryInfo `json:"registries"`
}

type RegistryInfo struct {
	Alias  string `json:"alias"`
	Origin string `json:"origin"`
}

type ServerInfo struct {
	SchemaVersion int        `json:"schema_version"`
	Server        BuildInfo  `json:"server"`
	APIVersion    string     `json:"api_version"`
	Config        ConfigInfo `json:"config"`
}

type Info struct {
	ServerInfo
	Client BuildInfo `json:"client"`
}

func (c *Client) Info() (ServerInfo, error) {
	var out ServerInfo
	err := c.do("GET", "/v1/info", nil, &out)
	return out, err
}
