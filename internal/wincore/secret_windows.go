//go:build windows

package wincore

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows 凭据管理器(控制面板 → 凭据管理器 → Windows 凭据,条目名 CommBox/<设置名>)。
var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
	credMaxBlob             = 5 * 512 // CRED_MAX_CREDENTIAL_BLOB_SIZE
)

// credentialW 对应 CREDENTIALW。
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type windowsCredentialStore struct{}

func platformSecretStore() SecretStore { return windowsCredentialStore{} }

func credTarget(name string) (*uint16, error) {
	return windows.UTF16PtrFromString("CommBox/" + name)
}

func (windowsCredentialStore) Get(name string) (string, bool, error) {
	target, err := credTarget(name)
	if err != nil {
		return "", false, err
	}
	var cred *credentialW
	r, _, callErr := procCredReadW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&cred)))
	if r == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return "", false, nil
		}
		return "", false, callErr
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(cred)))
	if cred.CredentialBlobSize == 0 || cred.CredentialBlob == nil {
		return "", true, nil
	}
	return string(unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)), true, nil
}

func (windowsCredentialStore) Set(name, value string) error {
	if len(value) > credMaxBlob {
		return errors.New("凭据内容过长")
	}
	target, err := credTarget(name)
	if err != nil {
		return err
	}
	user, _ := windows.UTF16PtrFromString("CommBox")
	blob := []byte(value)
	cred := credentialW{Type: credTypeGeneric, TargetName: target, Persist: credPersistLocalMachine, UserName: user,
		CredentialBlobSize: uint32(len(blob))}
	if len(blob) > 0 {
		cred.CredentialBlob = &blob[0]
	}
	if r, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0); r == 0 {
		return callErr
	}
	return nil
}

func (windowsCredentialStore) Delete(name string) error {
	target, err := credTarget(name)
	if err != nil {
		return err
	}
	if r, _, callErr := procCredDeleteW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0); r == 0 && !errors.Is(callErr, windows.ERROR_NOT_FOUND) {
		return callErr
	}
	return nil
}
