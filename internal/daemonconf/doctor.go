package daemonconf

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

// Check is one diagnostic result.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// DiagnoseInput is what Diagnose compares against reality.
type DiagnoseInput struct {
	ProxyURL string
	// Info is the dockerd /info response. InfoErr is set when it could not be fetched.
	Info    Info
	InfoErr error
	// Expected are the files the current configuration should have produced.
	Expected []File
	// MirrorSettings is a target capability, independent of the image store.
	MirrorSettings bool
}

// Diagnose runs read-only checks. It never changes anything.
func Diagnose(ctx context.Context, in DiagnoseInput) []Check {
	var checks []Check

	checks = append(checks, checkProxy(ctx, in.ProxyURL))

	if in.InfoErr != nil {
		checks = append(checks, Check{Name: "dockerd", OK: false, Detail: in.InfoErr.Error()})
		return append(checks, checkFiles(in.Expected)...)
	}
	store := DetectStore(in.Info)
	checks = append(checks, Check{Name: "dockerd image store", OK: true, Detail: string(store)})

	if store == StoreLegacy || in.MirrorSettings {
		mirrors := []string{}
		if in.Info.RegistryConfig != nil {
			mirrors = in.Info.RegistryConfig.Mirrors
		}
		ok := slices.ContainsFunc(mirrors, func(m string) bool { return strings.TrimRight(m, "/") == strings.TrimRight(in.ProxyURL, "/") })
		detail := fmt.Sprintf("dockerd reports mirrors %v", mirrors)
		checks = append(checks, Check{Name: "dockerd registry-mirrors", OK: ok, Detail: detail})
	}
	return append(checks, checkFiles(in.Expected)...)
}

func checkProxy(ctx context.Context, proxyURL string) Check {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyURL+"/v2/", nil)
	if err != nil {
		return Check{Name: "proxy", OK: false, Detail: err.Error()}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Check{Name: "proxy", OK: false, Detail: fmt.Sprintf("%s is not answering: %v", proxyURL, err)}
	}
	defer resp.Body.Close()
	return Check{Name: "proxy", OK: resp.StatusCode == http.StatusOK, Detail: fmt.Sprintf("GET %s/v2/ returned %s", proxyURL, resp.Status)}
}

func checkFiles(files []File) []Check {
	checks := make([]Check, 0, len(files))
	for _, f := range files {
		current, err := os.ReadFile(f.Path)
		switch {
		case err != nil:
			checks = append(checks, Check{Name: "file " + f.Path, OK: false, Detail: "missing: " + err.Error()})
		case !bytes.Equal(current, f.Content):
			checks = append(checks, Check{Name: "file " + f.Path, OK: false, Detail: "content differs from the current configuration; run dockerd apply"})
		default:
			checks = append(checks, Check{Name: "file " + f.Path, OK: true, Detail: "matches the current configuration"})
		}
	}
	return checks
}
