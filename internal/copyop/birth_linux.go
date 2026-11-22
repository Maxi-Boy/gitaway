//go:build linux && (amd64 || arm64)

package copyop

import (
	"encoding/binary"
	"syscall"
	"time"
	"unsafe"
)

const (
	statxBtime        = 0x800 // STATX_BTIME
	atFDCWD           = -100  // AT_FDCWD
	atSymlinkNoFollow = 0x100 // AT_SYMLINK_NOFOLLOW
)

// birthTime возвращает время создания файла через системный вызов statx (Linux 4.11+).
// Если файловая система его не хранит (например, NFS или старые ext4), возвращается false.
// Номер вызова sysStatx задан в birth_linux_amd64.go и birth_linux_arm64.go.
func birthTime(path string) (time.Time, bool) {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return time.Time{}, false
	}
	var buf [256]byte // struct statx занимает 256 байт
	dirfd := atFDCWD
	_, _, errno := syscall.Syscall6(sysStatx, uintptr(dirfd), uintptr(unsafe.Pointer(p)),
		atSymlinkNoFollow, statxBtime, uintptr(unsafe.Pointer(&buf[0])), 0)
	if errno != 0 {
		return time.Time{}, false
	}
	if binary.NativeEndian.Uint32(buf[0:4])&statxBtime == 0 { // stx_mask
		return time.Time{}, false
	}
	sec := int64(binary.NativeEndian.Uint64(buf[0x50:0x58]))  // stx_btime.tv_sec
	nsec := int64(binary.NativeEndian.Uint32(buf[0x58:0x5c])) // stx_btime.tv_nsec
	if sec <= 0 {
		return time.Time{}, false
	}
	return time.Unix(sec, nsec), true
}
