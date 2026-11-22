//go:build !(linux && (amd64 || arm64))

package copyop

import "time"

// birthTime: на этой платформе время создания файла недоступно.
func birthTime(path string) (time.Time, bool) { return time.Time{}, false }
