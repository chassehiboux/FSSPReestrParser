package platform

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// ChooseFolder использует современный IFileOpenDialog в режиме выбора папки.
// Порядок методов соответствует IUnknown → IModalWindow → IFileDialog.
var RegistrySourceRoot string

func ChooseFolder(owner uintptr, title, initialDir string) (string, bool, error) {
	hr := win.CoInitializeEx(nil, 2)
	if hr < 0 {
		return "", false, fmt.Errorf("не удалось открыть выбор папки: COM 0x%08x", uint32(hr))
	}
	defer win.CoUninitialize()
	clsid := windows.GUID{Data1: 0xdc1c5a9c, Data2: 0xe88a, Data3: 0x4dde, Data4: [8]byte{0xa5, 0xa1, 0x60, 0xf8, 0x2a, 0x20, 0xae, 0xf7}}
	iid := windows.GUID{Data1: 0xd57c7288, Data2: 0xd4ad, Data3: 0x4768, Data4: [8]byte{0xbe, 0x02, 0x9d, 0x96, 0x95, 0x32, 0xd9, 0x60}}
	type comObject struct{ Methods *[29]uintptr }
	var ptr unsafe.Pointer
	create := windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance")
	status, _, _ := create.Call(uintptr(unsafe.Pointer(&clsid)), 0, 1, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&ptr)))
	if int32(status) < 0 {
		return "", false, fmt.Errorf("выбор папки: 0x%08x", uint32(status))
	}
	dialog := (*comObject)(ptr)
	call := func(method int, args ...uintptr) uint32 {
		args = append([]uintptr{uintptr(ptr)}, args...)
		r, _, _ := syscall.SyscallN(dialog.Methods[method], args...)
		return uint32(r)
	}
	defer call(2)
	var flags uint32
	if r := call(10, uintptr(unsafe.Pointer(&flags))); int32(r) < 0 {
		return "", false, fmt.Errorf("параметры выбора папки: 0x%08x", r)
	}
	// FOS_PICKFOLDERS | FOS_FORCEFILESYSTEM | FOS_PATHMUSTEXIST | FOS_NOCHANGEDIR.
	if r := call(9, uintptr(flags|0x20|0x40|0x800|0x8)); int32(r) < 0 {
		return "", false, fmt.Errorf("режим выбора папки: 0x%08x", r)
	}
	caption, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return "", false, err
	}
	titleResult := call(17, uintptr(unsafe.Pointer(caption)))
	runtime.KeepAlive(caption)
	if r := titleResult; int32(r) < 0 {
		return "", false, fmt.Errorf("заголовок выбора папки: 0x%08x", r)
	}
	if initialDir != "" {
		name, e := windows.UTF16PtrFromString(initialDir)
		if e != nil {
			return "", false, e
		}
		shellIID := windows.GUID{Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee, Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}
		var initial unsafe.Pointer
		createItem := windows.NewLazySystemDLL("shell32.dll").NewProc("SHCreateItemFromParsingName")
		r, _, _ := createItem.Call(uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&shellIID)), uintptr(unsafe.Pointer(&initial)))
		runtime.KeepAlive(name)
		if int32(r) >= 0 {
			item := (*comObject)(initial)
			setResult := call(12, uintptr(initial))
			syscall.SyscallN(item.Methods[2], uintptr(initial))
			if int32(setResult) < 0 {
				return "", false, fmt.Errorf("начальная папка: 0x%08x", setResult)
			}
		}
	}
	if r := call(3, owner); r == 0x800704c7 {
		return "", false, nil
	} else if int32(r) < 0 {
		return "", false, fmt.Errorf("выбор папки: 0x%08x", r)
	}
	var itemPtr unsafe.Pointer
	if r := call(20, uintptr(unsafe.Pointer(&itemPtr))); int32(r) < 0 {
		return "", false, fmt.Errorf("результат выбора папки: 0x%08x", r)
	}
	item := (*comObject)(itemPtr)
	defer syscall.SyscallN(item.Methods[2], uintptr(itemPtr))
	var text *uint16
	r, _, _ := syscall.SyscallN(item.Methods[5], uintptr(itemPtr), 0x80058000, uintptr(unsafe.Pointer(&text))) // SIGDN_FILESYSPATH
	if int32(r) < 0 {
		return "", false, fmt.Errorf("путь выбранной папки: 0x%08x", uint32(r))
	}
	defer win.CoTaskMemFree(uintptr(unsafe.Pointer(text)))
	return windows.UTF16PtrToString(text), true, nil
}
