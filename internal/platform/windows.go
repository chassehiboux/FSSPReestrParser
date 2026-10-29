package platform

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// AdminSID задаётся при сборке и не читается из редактируемых общих настроек.
var AdminSID string

func LocalDir() (string, error) {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		return "", fmt.Errorf("Не найдена локальная папка пользователя")
	}
	p := filepath.Join(root, "FSSPReestrParser")
	return p, os.MkdirAll(p, 0700)
}
func Downloads() (string, error) { return windows.KnownFolderPath(windows.FOLDERID_Downloads, 0) }
func IsAdmin() bool {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	return e == nil && AdminSID != "" && u.User.Sid.String() == AdminSID
}
func OpenFolder(path string) error { return exec.Command("explorer.exe", path).Start() }

type blob struct {
	Size uint32
	Data *byte
}

var crypt32 = windows.NewLazySystemDLL("crypt32.dll")
var protect = crypt32.NewProc("CryptProtectData")
var unprotect = crypt32.NewProc("CryptUnprotectData")

func transform(data []byte, encrypt bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("Пустые защищаемые данные")
	}
	in := blob{Size: uint32(len(data)), Data: &data[0]}
	var out blob
	proc := protect
	if !encrypt {
		proc = unprotect
	}
	ok, _, e := proc.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(data)
	if ok == 0 {
		return nil, e
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
func WriteProtected(path string, value any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	b, e := transform(raw, true)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".secret-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(name, path)
}
func ReadProtected(path string, value any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	raw, e := transform(b, false)
	if e != nil {
		return e
	}
	return json.Unmarshal(raw, value)
}

func BrowserPath() (string, error) {
	for _, app := range []string{`Google\Chrome\Application\chrome.exe`, `Microsoft\Edge\Application\msedge.exe`} {
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			if root := os.Getenv(env); root != "" {
				p := filepath.Join(root, app)
				if st, e := os.Stat(p); e == nil && !st.IsDir() {
					return p, nil
				}
			}
		}
	}
	for _, name := range []string{"chrome.exe", "msedge.exe"} {
		if p, e := exec.LookPath(name); e == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("Не найден Chrome или Microsoft Edge. Установите один из браузеров")
}
func AccountLabel() string {
	return strings.Trim(os.Getenv("USERDOMAIN")+`\`+os.Getenv("USERNAME"), `\`)
}
