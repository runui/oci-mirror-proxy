package admin

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/runui/oci-mirror-proxy/internal/unixhttp"
)

func TestWindowsPipeLongPathAndPrivateDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), strings.Repeat("long", 40), "admin.sock")
	pipe, err := unixhttp.PipePath(path)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })}
	go srv.Serve(ln)
	defer srv.Close()
	client := unixhttp.Client(path, 0)
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://admin/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.Status)
	}
	name, err := windows.UTF16PtrFromString(pipe)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sddl := sd.String()
	if !strings.Contains(sddl, "D:P") || (!strings.Contains(sddl, "(A;;GA;;;"+user.User.Sid.String()+")") && !strings.Contains(sddl, "(A;;FA;;;"+user.User.Sid.String()+")")) || strings.Count(sddl, "(A;") != 1 {
		t.Fatalf("pipe must only grant the current user access: %s", sddl)
	}
}

func TestWindowsRejectsRemoteAdminPipe(t *testing.T) {
	if ln, err := Listen(`\\remote\pipe\admin`); err == nil {
		ln.Close()
		t.Fatal("remote pipe accepted")
	}
}
