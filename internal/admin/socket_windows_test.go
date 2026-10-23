package admin

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/runui/oci-mirror-proxy/internal/unixhttp"
)

func TestWindowsSystemPipeDACL(t *testing.T) {
	owner, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	sddl := pipeSecurityDescriptor(owner)
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 3 {
		t.Fatalf("unexpected system DACL: %s, %v", sddl, err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 2, &ace); err != nil {
		t.Fatal(err)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	const clientAccess = (windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE) &^ windows.FILE_APPEND_DATA
	if !sid.IsWellKnown(windows.WinBuiltinUsersSid) || ace.Mask != clientAccess || ace.Mask&(windows.WRITE_DAC|windows.WRITE_OWNER|windows.FILE_APPEND_DATA) != 0 {
		t.Fatalf("unsafe client ACE: %s", sddl)
	}
}

func TestWindowsRestrictedClientPipeAccess(t *testing.T) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// Grant the test owner server rights, but inspect a separate client-only ACE
	// above. Exercise client I/O with the exact reduced access mask here.
	path := filepath.Join(t.TempDir(), "restricted.sock")
	ln, err := winio.ListenPipe(mustPipePath(t, path), &winio.PipeConfig{SecurityDescriptor: pipeSecurityDescriptor(user.User.Sid)})
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })}
	go srv.Serve(ln)
	defer srv.Close()
	client := unixhttp.Client(path, 0)
	defer client.CloseIdleConnections()
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://admin/", nil)
	resp, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.Status)
	}
}

func mustPipePath(t *testing.T, path string) string {
	t.Helper()
	pipe, err := unixhttp.PipePath(path)
	if err != nil {
		t.Fatal(err)
	}
	return pipe
}

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
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 || dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("pipe must have a protected DACL with one ACE: %s", sd.String())
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	// SDDL can abbreviate the current user's SID (for example, as LA).
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 || !sid.Equals(user.User.Sid) || (ace.Mask != windows.GENERIC_ALL && ace.Mask != fileAllAccess) {
		t.Fatalf("pipe must only grant the current user full access: %s", sd.String())
	}
}

func TestWindowsRejectsRemoteAdminPipe(t *testing.T) {
	if ln, err := Listen(`\\remote\pipe\admin`); err == nil {
		ln.Close()
		t.Fatal("remote pipe accepted")
	}
}
