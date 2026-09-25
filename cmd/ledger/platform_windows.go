package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const executableSuffix = ".exe"

// Only the current Windows user and SYSTEM may access credential files.
func privateSecurity() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)")
}

func privateDir(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	sd, err := privateSecurity()
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err = windows.CreateDirectory(name, &sa); err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return err
	}
	f, err := openNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("credential directory must be a real directory")
	}
	return checkPrivate(f)
}

func openNoFollow(path string, flag int, _ os.FileMode) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	sd, err := privateSecurity()
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	access := uint32(windows.GENERIC_READ)
	if flag&(os.O_RDWR|os.O_WRONLY) != 0 {
		access |= windows.GENERIC_WRITE
	}
	disposition := uint32(windows.OPEN_EXISTING)
	if flag&os.O_CREATE != 0 {
		disposition = windows.OPEN_ALWAYS
	}
	if flag&os.O_EXCL != 0 {
		disposition = windows.CREATE_NEW
	}
	handle, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, &sa, disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(handle), path)
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
		f.Close()
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		f.Close()
		return nil, errors.New("refusing to follow a reparse point")
	}
	if flag&os.O_TRUNC != 0 {
		if err = f.Truncate(0); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

func checkPrivate(f *os.File) error {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if owner == nil || !owner.Equals(user.User.Sid) {
		return errors.New("credential owner must be the current Windows user")
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if acl == nil {
		return errors.New("credentials must have a private Windows ACL")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || (!sid.Equals(user.User.Sid) && !sid.IsWellKnown(windows.WinLocalSystemSid)) {
			return errors.New("credentials must be accessible only to the current Windows user and SYSTEM")
		}
	}
	return nil
}

func privateTemp(dir, pattern string) (*os.File, error) {
	for range 8 {
		name := filepath.Join(dir, strings.Replace(pattern, "*", rand.Text(), 1))
		f, err := openNoFollow(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if !errors.Is(err, os.ErrExist) {
			return f, err
		}
	}
	return nil, errors.New("could not create private temporary file")
}

func withLock(ctx context.Context, path string, fn func() error) error {
	f, err := openNoFollow(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	var overlapped windows.Overlapped
	for {
		err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		if err == nil {
			break
		}
		if err != windows.ERROR_LOCK_VIOLATION {
			return err
		}
		if err = waitFor(ctx, 25*time.Millisecond); err != nil {
			return errors.New("credentials busy; retry shortly")
		}
	}
	defer windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
	return fn()
}

func replaceFile(from, to string) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func replaceExecutable(from, to string) error {
	// Windows permits renaming a running image, but not overwriting it.
	// Retain one rollback copy until the next update, when the old process has exited.
	backup := to + ".old"
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := replaceFile(to, backup); err != nil {
		return err
	}
	if err := replaceFile(from, to); err != nil {
		return errors.Join(err, replaceFile(backup, to))
	}
	return nil
}

// MoveFileEx uses WRITE_THROUGH; Windows does not support fsync on directory handles.
func syncDirectory(string) error { return nil }
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS, HideWindow: true}
}

func powershellCommand(script string) string {
	words := utf16.Encode([]rune(script))
	data := make([]byte, 2*len(words))
	for i, word := range words {
		binary.LittleEndian.PutUint16(data[2*i:], word)
	}
	return base64.StdEncoding.EncodeToString(data)
}
func powershellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func headerHelper(exe, profile, dir string) string {
	// Codex launches through cmd.exe. Encoding avoids cmd expansion of %, &, and paths with spaces.
	script := "& " + powershellQuote(exe) + " auth headers --profile " + powershellQuote(profile) + " --credential-dir " + powershellQuote(dir) + "; exit $LASTEXITCODE"
	return "powershell.exe -NoLogo -NoProfile -NonInteractive -EncodedCommand " + powershellCommand(script)
}

var getCurrentPackageFamilyName = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentPackageFamilyName")

// packageFamilyName reports the package identity that packaged apps, such as the
// Codex desktop app, pass on to the processes they start.
var packageFamilyName = func() (string, bool) {
	if getCurrentPackageFamilyName.Find() != nil {
		return "", false
	}
	var length uint32
	rc, _, _ := getCurrentPackageFamilyName.Call(uintptr(unsafe.Pointer(&length)), 0)
	if syscall.Errno(rc) != windows.ERROR_INSUFFICIENT_BUFFER || length == 0 {
		return "", false
	}
	name := make([]uint16, length)
	if rc, _, _ = getCurrentPackageFamilyName.Call(uintptr(unsafe.Pointer(&length)), uintptr(unsafe.Pointer(&name[0]))); rc != 0 {
		return "", false
	}
	return windows.UTF16ToString(name), true
}

// Windows virtualizes %APPDATA% for packaged apps: files a login writes there land
// under the package's LocalCache, which an ordinary terminal never consults.
// Resolving that redirected directory first lets the helper name the location the
// login actually used.
func configDir() (string, error) {
	family, ok := packageFamilyName()
	if !ok {
		return os.UserConfigDir()
	}
	local, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(local, "Packages", family, "LocalCache", "Roaming"), nil
}

func codexCommand(ctx context.Context, args ...string) *exec.Cmd {
	path, err := exec.LookPath("codex")
	if err != nil || strings.EqualFold(filepath.Ext(path), ".exe") {
		return exec.CommandContext(ctx, "codex", args...)
	}
	script := "& " + powershellQuote(path)
	for _, arg := range args {
		script += " " + powershellQuote(arg)
	}
	return exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", powershellCommand(script+"; exit $LASTEXITCODE"))
}
