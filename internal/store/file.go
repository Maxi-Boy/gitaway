package store

import (
	"encoding/json"
	"os"
)

// writeJSONFile пишет v во временный файл рядом с path и переименовывает его на место,
// чтобы сбой посреди записи не оставил испорченный файл. Права 0600.
func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
