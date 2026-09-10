package store

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func trustedRecoveryDirectory(f *os.File) bool {
	i, e := f.Stat()
	return e == nil && i.IsDir() && protectedRecoveryKey(f)
}

func trustedRecoveryAncestor(f *os.File) bool {
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return false
	}
	trusted := func(sid *windows.SID) bool {
		return sid != nil && (sid.Equals(user.User.Sid) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) || sid.String() == "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464")
	}
	sd, e := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if e != nil {
		return false
	}
	owner, _, e := sd.Owner()
	if e != nil || !trusted(owner) {
		return false
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil {
		return false
	}
	const fileDeleteChild = 0x0040
	const unsafeRights = windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | fileDeleteChild | windows.FILE_WRITE_DATA | windows.FILE_WRITE_ATTRIBUTES | windows.FILE_WRITE_EA | windows.GENERIC_ALL | windows.GENERIC_WRITE
	for n := uint32(0); n < uint32(acl.AceCount); n++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(acl, n, &ace); e != nil {
			return false
		}
		if ace.Header.AceFlags&0x08 != 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return false
		}
		if ace.Mask&unsafeRights != 0 && !trusted((*windows.SID)(unsafe.Pointer(&ace.SidStart))) { // #nosec G103 -- GetAce returns a validated ACCESS_ALLOWED_ACE; SidStart is its documented inline SID.
			return false
		}
	}
	return true
}

func protectedRecoveryKey(f *os.File) bool {
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return false
	}
	sd, e := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if e != nil {
		return false
	}
	owner, _, e := sd.Owner()
	if e != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return false
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil {
		return false
	}
	for n := uint32(0); n < uint32(acl.AceCount); n++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(acl, n, &ace); e != nil {
			return false
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE || ace.Header.AceFlags&0x08 != 0 {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return false
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) // #nosec G103 -- GetAce returns a validated ACCESS_ALLOWED_ACE; SidStart is its documented inline SID.
		if !sid.Equals(user.User.Sid) && !sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return false
		}
	}
	return true
}

func singleRecoveryLink(f *os.File) bool {
	var i windows.ByHandleFileInformation
	return windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &i) == nil && i.NumberOfLinks == 1
}
func protectRecoveryPath(path string, dir bool) error {
	token := windows.GetCurrentProcessToken()
	user, e := token.GetTokenUser()
	if e != nil {
		return e
	}
	flags := ""
	if dir {
		flags = "OICI"
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;" + flags + ";FA;;;" + user.User.Sid.String() + ")")
	if e != nil {
		return e
	}
	acl, _, e := sd.DACL()
	if e != nil {
		return e
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// Windows does not expose portable directory fsync. Flush the completed files
// before rename; the directory move remains atomic within the volume.
func syncRecoveryDirectory(path string) error { return nil }
